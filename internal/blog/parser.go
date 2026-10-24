package blog

import (
	"bufio"
	"bytes"
	"fmt"
	stdhtml "html"
	"strings"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/sagnikb/myspace/internal/models"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// md renders every post: front matter, then the markdown with the body's
// typography (typesetting).
var md = newMarkdown(append([]goldmark.Extender{meta.Meta}, typesetting()...)...)

// bodyMD parses a post's body without its front matter (Document).
var bodyMD = newMarkdown(typesetting()...)

// newMarkdown returns the goldmark configuration every post is rendered with,
// plus the extra extensions: md adds front matter and typography; tests leave
// typography out to compare against.
func newMarkdown(extra ...goldmark.Extender) goldmark.Markdown {
	extensions := append([]goldmark.Extender{
		extension.GFM,
		highlighting.NewHighlighting(
			highlighting.WithFormatOptions(
				chromahtml.WithClasses(true),
				chromahtml.WithAllClasses(true),
				chromahtml.WithPreWrapper(codePre{}),
			),
			highlighting.WithWrapperRenderer(codeBlockWrapper),
		),
	}, extra...)
	return goldmark.New(
		goldmark.WithExtensions(extensions...),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
			// Self-closing void tags keep HTMLContent valid XHTML for EPUB export.
			html.WithXHTML(),
		),
	)
}

// Document parses a post's markdown body (Blog.Content, without front
// matter) into the tree Parse renders it from: the same extensions,
// typography and heading IDs. Exporters walk it; the nodes' segments point
// into the returned source.
func Document(content string) (ast.Node, []byte) {
	source := []byte(content)
	return bodyMD.Parser().Parse(text.NewReader(source)), source
}

func Parse(source []byte) (*models.Blog, error) {
	ctx := parser.NewContext()
	doc := md.Parser().Parse(text.NewReader(source), parser.WithContext(ctx))

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, source, doc); err != nil {
		return nil, fmt.Errorf("render markdown: %w", err)
	}

	metadata, err := meta.TryGet(ctx)
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}

	blog := &models.Blog{
		HTMLContent: buf.String(),
		Content:     stripFrontmatter(source),
		PlainText:   plainText(doc, source),
		CodeText:    codeText(doc, source),
		TOC:         extractTOC(doc, source),
		Status:      "published",
	}

	// Front matter never reaches the Typographer: typeset gives these
	// fields the body's punctuation.
	if v, ok := metadata["title"].(string); ok {
		blog.Title = typeset(v)
	}
	if v, ok := metadata["description"].(string); ok {
		blog.Description = typeset(v)
	}
	if v, ok := metadata["pullquote"].(string); ok {
		blog.Pullquote = typeset(strings.TrimSpace(v))
	}
	if v, ok := metadata["status"].(string); ok {
		blog.Status = v
	}

	switch v := metadata["date"].(type) {
	case string:
		if t, err := time.Parse("2006-01-02", v); err == nil {
			blog.Date = t
		}
	case time.Time:
		blog.Date = v
	}

	switch v := metadata["tags"].(type) {
	case []interface{}:
		for _, item := range v {
			if tag, ok := item.(string); ok {
				blog.Tags = appendTag(blog.Tags, tag)
			}
		}
	case string:
		for _, tag := range strings.Split(v, ",") {
			blog.Tags = appendTag(blog.Tags, tag)
		}
	}

	blog.ReadingTime = estimateReadingTime(blog.Content)

	return blog, nil
}

func appendTag(tags []string, tag string) []string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return tags
	}
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func estimateReadingTime(text string) int {
	words := len(strings.Fields(text))
	minutes := words / 200
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}

// extractTOC lists the document's top-level h2 and h3 headings with the IDs
// goldmark assigned while parsing, so every entry points at a rendered anchor.
func extractTOC(doc ast.Node, source []byte) []models.TOCItem {
	var items []models.TOCItem
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || h.Level < 2 || h.Level > 3 {
			continue
		}
		id := headingID(h)
		title := inlineText(h, source)
		if id == "" || title == "" {
			continue
		}
		items = append(items, models.TOCItem{Level: h.Level, ID: id, Title: title})
	}
	return items
}

func headingID(h *ast.Heading) string {
	v, ok := h.AttributeString("id")
	if !ok {
		return ""
	}
	switch id := v.(type) {
	case []byte:
		return string(id)
	case string:
		return id
	}
	return ""
}

// inlineText returns the plain text of n's children on a single line.
func inlineText(n ast.Node, source []byte) string {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		writeText(w, c, source)
	}
	_ = w.Flush()
	return collapseSpace(stdhtml.UnescapeString(buf.String()))
}

// plainText returns the prose of doc without markdown syntax, one block per
// line: headings, paragraphs, list items, quotes and table cells. Code blocks
// and raw HTML are left out; codeText has the code.
func plainText(doc ast.Node, source []byte) string {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	writeText(w, doc, source)
	_ = w.Flush()
	return joinLines(strings.Split(stdhtml.UnescapeString(buf.String()), "\n"))
}

// codeText returns the contents of doc's code blocks, one code line per
// line, so identifiers that only appear in code stay searchable.
func codeText(doc ast.Node, source []byte) string {
	var lines []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			if entering {
				for i := 0; i < n.Lines().Len(); i++ {
					line := n.Lines().At(i)
					lines = append(lines, string(line.Value(source)))
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return joinLines(lines)
}

// joinLines collapses the spacing inside each line, drops empty lines and
// joins the rest with newlines.
func joinLines(lines []string) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		for _, l := range strings.Split(line, "\n") {
			if l = collapseSpace(l); l != "" {
				out = append(out, l)
			}
		}
	}
	return strings.Join(out, "\n")
}

// collapseSpace trims s and collapses its runs of ASCII spacing to single
// spaces. A no-break space is text, not spacing: it stays where finish put it.
func collapseSpace(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
	}), " ")
}

// writeText writes the text of n and its descendants HTML-escaped, exactly as
// goldmark's renderer would emit it (escapes and entities resolved) but
// without any markup or code blocks, so callers get plain text by unescaping
// the result once.
func writeText(w util.BufWriter, n ast.Node, source []byte) {
	switch n := n.(type) {
	case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock, *ast.RawHTML:
		return
	case *ast.Text:
		value := n.Segment.Value(source)
		if n.IsRaw() {
			html.DefaultWriter.RawWrite(w, value)
		} else {
			html.DefaultWriter.Write(w, value)
		}
		switch {
		case n.HardLineBreak():
			_ = w.WriteByte('\n')
		case n.SoftLineBreak():
			_ = w.WriteByte(' ')
		}
		return
	case *ast.String:
		switch {
		case n.IsCode():
			_, _ = w.Write(n.Value)
		case n.IsRaw():
			html.DefaultWriter.RawWrite(w, n.Value)
		default:
			html.DefaultWriter.Write(w, n.Value)
		}
		return
	case *ast.AutoLink:
		html.DefaultWriter.RawWrite(w, n.Label(source))
		return
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		writeText(w, c, source)
	}
	if n.Type() == ast.TypeBlock {
		_ = w.WriteByte('\n')
	}
}

// stripFrontmatter returns source without its leading YAML block, using the
// same separator rule as goldmark-meta (a line made only of dashes).
func stripFrontmatter(source []byte) string {
	s := string(source)
	first, rest, ok := strings.Cut(s, "\n")
	if !ok || !isSeparator(first) {
		return s
	}
	for rest != "" {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		if isSeparator(line) {
			return rest
		}
	}
	return s
}

func isSeparator(line string) bool {
	line = strings.TrimSpace(line)
	return line != "" && strings.Trim(line, "-") == ""
}

// plainPreOpen must match what codePre emits for chroma's ` class="chroma"`.
const plainPreOpen = `<pre tabindex="0" class="chroma"><code>`

// codePre makes chroma emit <pre tabindex="0" class="chroma"><code>, so
// scrollable code blocks are keyboard-focusable.
type codePre struct{}

func (codePre) Start(code bool, styleAttr string) string {
	if code {
		return `<pre tabindex="0"` + styleAttr + `><code>`
	}
	return `<pre` + styleAttr + `>`
}

func (codePre) End(code bool) string {
	if code {
		return `</code></pre>`
	}
	return `</pre>`
}

// codeBlockWrapper renders the frame around every fenced code block. For
// blocks chroma cannot highlight, goldmark-highlighting writes only the
// escaped code, so the wrapper also supplies the <pre><code> element.
func codeBlockWrapper(w util.BufWriter, ctx highlighting.CodeBlockContext, entering bool) {
	if !entering {
		if !ctx.Highlighted() {
			_, _ = w.WriteString("</code></pre>")
		}
		_, _ = w.WriteString("\n</div>\n")
		return
	}
	lang := stdhtml.EscapeString(codeLanguage(ctx))
	_, _ = w.WriteString(`<div class="code-block" data-lang="` + lang + "\">\n" +
		`<div class="code-block__header"><span class="code-block__lang">` + lang +
		`</span><button type="button" class="code-block__copy" hidden="">Copy</button></div>` + "\n")
	if !ctx.Highlighted() {
		_, _ = w.WriteString(plainPreOpen)
	}
}

func codeLanguage(ctx highlighting.CodeBlockContext) string {
	if !ctx.Highlighted() {
		return "text"
	}
	if lang, ok := ctx.Language(); ok && len(lang) > 0 {
		return strings.ToLower(string(lang))
	}
	return "text"
}
