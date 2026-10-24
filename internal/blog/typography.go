package blog

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// typography is what the Typographer extension writes for each ASCII
// punctuation pattern. goldmark's defaults are HTML named entities (&rsquo;
// and so on); these are the UTF-8 characters instead, because HTMLContent is
// also an EPUB chapter (XHTML, where those entities are undefined) and the TOC
// titles and search text are built from the same AST and must contain the
// characters themselves. Code spans and code blocks are never touched.
var typography = map[extension.TypographicPunctuation]string{
	extension.LeftSingleQuote:  "‘", // ‘
	extension.RightSingleQuote: "’", // ’
	extension.LeftDoubleQuote:  "“", // “
	extension.RightDoubleQuote: "”", // ”
	extension.EnDash:           "–", // –
	extension.EmDash:           "—", // —
	extension.Ellipsis:         "…", // …
	extension.LeftAngleQuote:   "«", // «
	extension.RightAngleQuote:  "»", // »
	extension.Apostrophe:       "’", // ’
}

// typesetting is the body's typography: the Typographer, then finish on every
// text node (finisher).
func typesetting() []goldmark.Extender {
	return []goldmark.Extender{
		extension.NewTypographer(extension.WithTypographicSubstitutions(typography)),
		finishing{},
	}
}

const (
	nbsp   = '\u00A0'
	enDash = '\u2013'
	emDash = '\u2014'
)

// dashes are the Typographer's dash and ellipsis rules; the longest pattern
// is listed first, so "---" is an em dash rather than an en dash and a hyphen.
var dashes = strings.NewReplacer("---", "—", "--", "–", "...", "…")

// typeset sets a plain front-matter string (a title, description or
// pullquote), which never reaches the Typographer, the way the body is set:
// --- and -- become — and –, ... becomes …, an apostrophe inside a word or
// before a decade ('90s) becomes ’, paired quotes become “ ” and ‘ ’, and
// finish's rules apply. A quote it cannot pair stays straight (a 21" screen);
// a closing single quote after a word is an apostrophe either way (Smiths').
// The result is plain UTF-8 text, so every output escapes it as usual.
func typeset(s string) string {
	return finish(smartQuotes(dashes.Replace(s)))
}

// smartQuotes curls the straight quotes in s (see typeset).
func smartQuotes(s string) string {
	if !strings.ContainsAny(s, `'"`) {
		return s
	}
	rs := []rune(s)
	at := func(i int) rune {
		if i < 0 || i >= len(rs) {
			return ' ' // the ends of the string count as spaces
		}
		return rs[i]
	}
	// opens and closes report whether the quote at i could open or close a
	// quotation: an opening quote follows a space or opening punctuation and
	// precedes a non-space; a closing quote follows a non-space and precedes
	// a space or punctuation.
	opens := func(i int) bool {
		p, n := at(i-1), at(i+1)
		return (unicode.IsSpace(p) || strings.ContainsRune("([{<—–-/“‘\"'", p)) && !unicode.IsSpace(n)
	}
	closes := func(i int) bool {
		p, n := at(i-1), at(i+1)
		return !unicode.IsSpace(p) && (unicode.IsSpace(n) || unicode.IsPunct(n))
	}
	open := map[rune]int{'"': -1, '\'': -1} // the unpaired opening quote of each kind
	for i, r := range rs {
		if r != '"' && r != '\'' {
			continue
		}
		p, n := at(i-1), at(i+1)
		if r == '\'' {
			// an apostrophe: inside a word (Go's, isn't, 90's) or before a
			// decade ('90s)
			if (unicode.IsLetter(p) || unicode.IsDigit(p)) && unicode.IsLetter(n) ||
				opens(i) && unicode.IsDigit(n) && unicode.IsDigit(at(i+2)) && at(i+3) == 's' && !unicode.IsLetter(at(i+4)) {
				rs[i] = '’'
				continue
			}
		}
		switch {
		case opens(i) && !closes(i):
			open[r] = i
		case closes(i) && !opens(i):
			if j := open[r]; j >= 0 {
				rs[j], rs[i] = '“', '”'
				if r == '\'' {
					rs[j], rs[i] = '‘', '’'
				}
				open[r] = -1
			} else if r == '\'' && (unicode.IsLetter(p) || unicode.IsDigit(p)) {
				rs[i] = '’' // plural possessive: Smiths'
			}
		}
	}
	return string(rs)
}

// finish applies two rules the Typographer lacks, to front matter (typeset)
// and to the body's text (finisher):
//
//   - A space before a spaced dash ("a — b", "a – b") becomes a no-break
//     space, so a line never starts with the dash.
//   - A hyphen between two numbers is a range and becomes an en dash: 5-10,
//     1.1-1.5, 50-75%, 10-50x, 9:00-17:00. A hyphen in a date (2026-01-15,
//     2026-01), in a name or version (x86-64, v1.2-3) or in a chain of
//     numbers (978-3-16) is left alone.
func finish(s string) string {
	if !strings.ContainsAny(s, " -") {
		return s
	}
	rs := []rune(s)
	changed := false
	for i, r := range rs {
		switch {
		case r == ' ' && i > 0 && !unicode.IsSpace(rs[i-1]) && i+1 < len(rs) &&
			(rs[i+1] == emDash || rs[i+1] == enDash) && (i+2 == len(rs) || unicode.IsSpace(rs[i+2])):
			rs[i], changed = nbsp, true
		case r == '-' && isRange(rs, i):
			rs[i], changed = enDash, true
		}
	}
	if !changed {
		return s
	}
	return string(rs)
}

// isRange reports whether the hyphen at rs[i] joins two numbers as a range
// (see finish). A number is digits with single '.', ',' or ':' between them.
func isRange(rs []rune, i int) bool {
	digit := func(j int) bool { return j >= 0 && j < len(rs) && '0' <= rs[j] && rs[j] <= '9' }
	inner := func(j int) bool { return j >= 0 && j < len(rs) && strings.ContainsRune(".,:", rs[j]) }

	start := i
	for digit(start-1) || inner(start-1) && digit(start-2) {
		start--
	}
	if start == i || !digit(start) {
		return false
	}
	if start > 0 {
		if p := rs[start-1]; unicode.IsLetter(p) || strings.ContainsRune("-–—_/\\:.#@&=+", p) {
			return false // x86-64, v1.2-3, 2026-01-15's second hyphen
		}
	}
	end := i + 1
	if !digit(end) {
		return false
	}
	for digit(end) || inner(end) && digit(end+1) {
		end++
	}
	if end < len(rs) && strings.ContainsRune("-–/", rs[end]) {
		return false // 2026-01-15, 978-3-16
	}
	// a year and a month (2026-01) is a date, not a range (2019-20 is one)
	left, right := string(rs[start:i]), string(rs[i+1:end])
	if len(left) == 4 && len(right) == 2 && (right[0] == '0' || right == "10" || right == "11" || right == "12") {
		return false
	}
	return true
}

// finishing is the goldmark extension that runs finisher after parsing.
type finishing struct{}

func (finishing) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithASTTransformers(util.Prioritized(finisher{}, 999)))
}

// finisher applies finish to the text of a parsed document: every text node
// outside code spans, raw HTML and autolinks (code blocks have none).
// goldmark splits a line's text into several sibling nodes at arbitrary
// points ("a 5" and "-10"), and the Typographer adds nodes of its own for the
// quotes and dashes it writes, so finish runs over each run of adjacent text
// nodes as one string. A text node it changes becomes a String node with the
// new text, which renders, escapes and unescapes exactly as the text did.
type finisher struct{}

func (finisher) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var texts []*ast.Text
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.CodeSpan, *ast.RawHTML, *ast.AutoLink, *ast.HTMLBlock, *ast.CodeBlock, *ast.FencedCodeBlock:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			if !n.IsRaw() {
				texts = append(texts, n)
			}
		}
		return ast.WalkContinue, nil
	})

	done := make(map[ast.Node]bool, len(texts))
	for _, t := range texts {
		if done[t] {
			continue
		}
		first := ast.Node(t)
		for p := first.PreviousSibling(); inRun(p); p = p.PreviousSibling() {
			first = p
		}
		// the run's text, with each node's span of runes in it
		type span struct {
			node       ast.Node
			value      string
			start, end int
		}
		var run []span
		var b strings.Builder
		pos := 0
		for n := first; inRun(n); n = n.NextSibling() {
			done[n] = true
			v := runValue(n, source)
			run = append(run, span{n, v, pos, pos + utf8.RuneCountInString(v)})
			b.WriteString(v)
			pos += utf8.RuneCountInString(v)
			if t, ok := n.(*ast.Text); ok && (t.SoftLineBreak() || t.HardLineBreak()) {
				b.WriteByte('\n')
				pos++
			}
		}
		finished := []rune(finish(b.String())) // finish keeps every rune's position
		for _, sp := range run {
			t, ok := sp.node.(*ast.Text)
			v := string(finished[sp.start:sp.end])
			if !ok || v == sp.value {
				continue
			}
			parent := t.Parent()
			s := ast.NewString([]byte(v))
			parent.ReplaceChild(parent, t, s)
			if t.SoftLineBreak() || t.HardLineBreak() {
				// the line break belonged to the text node: keep it, on an empty one
				br := ast.NewTextSegment(text.NewSegment(t.Segment.Stop, t.Segment.Stop))
				br.SetSoftLineBreak(t.SoftLineBreak())
				br.SetHardLineBreak(t.HardLineBreak())
				parent.InsertAfter(parent, s, br)
			}
		}
	}
}

// inRun reports whether n is text finish may read: a text node, or a quote
// or dash the Typographer wrote (a String node; it never changes).
func inRun(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.Text:
		return !n.IsRaw()
	case *ast.String:
		return n.IsCode()
	}
	return false
}

func runValue(n ast.Node, source []byte) string {
	if t, ok := n.(*ast.Text); ok {
		return string(t.Segment.Value(source))
	}
	return string(n.(*ast.String).Value)
}
