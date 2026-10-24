package search

import (
	"fmt"
	"html"
	"html/template"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sagnikb/myspace/internal/database"
)

const (
	maxInputRunes = 100
	maxTokens     = 8
	maxResults    = 20
	// minPrefixRunes is the shortest term that also matches as a prefix.
	minPrefixRunes = 3
)

// Result is a search match whose TitleHTML and Snippet are safe HTML: escaped
// text with matched terms wrapped in <mark></mark>.
type Result struct {
	Slug      string
	TitleHTML template.HTML
	Snippet   template.HTML
}

type Engine struct {
	db *database.DB
}

func New(db *database.DB) *Engine {
	return &Engine{db: db}
}

// Search matches raw user input against the index. Input without any word
// characters yields no results rather than an error.
func (e *Engine) Search(input string) ([]Result, error) {
	match := buildQuery(input)
	if match == "" {
		return nil, nil
	}
	rows, err := e.db.Search(match, maxResults)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", match, err)
	}
	results := make([]Result, 0, len(rows))
	for _, r := range rows {
		results = append(results, Result{
			Slug:      r.Slug,
			TitleHTML: highlightHTML(r.Title),
			Snippet:   highlightHTML(r.Snippet),
		})
	}
	return results, nil
}

// buildQuery turns arbitrary input into a safe FTS5 expression: each run of
// letters and digits becomes a quoted term and the terms are ANDed. A term
// of three or more characters also matches as a prefix ("contain" finds
// "containers"), but a whole-word match counts twice, so it ranks first:
// ("tok" OR "tok"*). A shorter term matches whole words only, so "go" never
// finds or highlights "good" or "goroutines". FTS5 syntax characters never
// reach the expression, so it cannot fail to parse. Returns "" when the input
// has no searchable terms.
func buildQuery(input string) string {
	if runes := []rune(input); len(runes) > maxInputRunes {
		input = string(runes[:maxInputRunes])
	}

	var terms []string
	seen := make(map[string]bool)
	for _, tok := range tokenize(input) {
		tok = strings.ToLower(tok)
		if seen[tok] {
			continue
		}
		seen[tok] = true
		if utf8.RuneCountInString(tok) < minPrefixRunes {
			terms = append(terms, `"`+tok+`"`)
		} else {
			terms = append(terms, `("`+tok+`" OR "`+tok+`"*)`)
		}
		if len(terms) == maxTokens {
			break
		}
	}
	return strings.Join(terms, " AND ")
}

// tokenize splits s into runs of letters and numbers. Combining marks are kept
// inside a token (as FTS5's unicode61 tokenizer does) but never start one.
func tokenize(s string) []string {
	var tokens []string
	var cur []rune
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			cur = append(cur, r)
		case unicode.IsMark(r) && len(cur) > 0:
			cur = append(cur, r)
		default:
			if len(cur) > 0 {
				tokens = append(tokens, string(cur))
				cur = cur[:0]
			}
		}
	}
	if len(cur) > 0 {
		tokens = append(tokens, string(cur))
	}
	return tokens
}

// highlightHTML escapes s and converts the database highlight markers into
// balanced <mark> tags; stray markers are dropped.
func highlightHTML(s string) template.HTML {
	var b strings.Builder
	open := false
	for len(s) > 0 {
		i := strings.IndexAny(s, database.MarkStart+database.MarkEnd)
		if i < 0 {
			b.WriteString(html.EscapeString(s))
			break
		}
		b.WriteString(html.EscapeString(s[:i]))
		switch s[i : i+1] {
		case database.MarkStart:
			if !open {
				b.WriteString("<mark>")
				open = true
			}
		case database.MarkEnd:
			if open {
				b.WriteString("</mark>")
				open = false
			}
		}
		s = s[i+1:]
	}
	if open {
		b.WriteString("</mark>")
	}
	return template.HTML(b.String())
}
