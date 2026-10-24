package blog

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/sagnikb/myspace/internal/models"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

const codeBlockHeader = `<div class="code-block__header"><span class="code-block__lang">%s</span><button type="button" class="code-block__copy" hidden="">Copy</button></div>`

func mustParse(t *testing.T, src string) *models.Blog {
	t.Helper()
	b, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return b
}

func realPosts(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob("../../content/blogs/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no posts found in content/blogs")
	}
	posts := make(map[string][]byte, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		posts[filepath.Base(p)] = data
	}
	return posts
}

func assertTOCAnchors(t *testing.T, b *models.Blog) {
	t.Helper()
	for _, item := range b.TOC {
		if !strings.Contains(b.HTMLContent, `id="`+item.ID+`"`) {
			t.Errorf("TOC entry %q links to #%s, which is not in the HTML", item.Title, item.ID)
		}
	}
}

func TestParseTOC(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []models.TOCItem
	}{
		{
			name: "code comments are not headings",
			src:  "## Setup\n\n```bash\n# Create two namespaces\n## also not a heading\nip netns add a\n```\n\n~~~\n# tilde fence\n~~~\n\n    # indented code\n\n### Next step\n",
			want: []models.TOCItem{
				{Level: 2, ID: "setup", Title: "Setup"},
				{Level: 3, ID: "next-step", Title: "Next step"},
			},
		},
		{
			name: "only levels 2 and 3",
			src:  "# One\n\n## Two\n\n### Three\n\n#### Four\n",
			want: []models.TOCItem{
				{Level: 2, ID: "two", Title: "Two"},
				{Level: 3, ID: "three", Title: "Three"},
			},
		},
		{
			name: "titles are plain text",
			src:  "## The `docker0` *bridge* & [NAT](https://example.com) \\*\n",
			want: []models.TOCItem{
				{Level: 2, ID: "the-docker0-bridge--nathttpsexamplecom-", Title: "The docker0 bridge & NAT *"},
			},
		},
		{
			name: "duplicate headings get unique ids",
			src:  "## FAQ\n\n## FAQ\n",
			want: []models.TOCItem{
				{Level: 2, ID: "faq", Title: "FAQ"},
				{Level: 2, ID: "faq-1", Title: "FAQ"},
			},
		},
		{
			name: "setext heading",
			src:  "Overview\n--------\n\ntext\n",
			want: []models.TOCItem{{Level: 2, ID: "overview", Title: "Overview"}},
		},
		{
			name: "headings nested in quotes are skipped",
			src:  "> ## Quoted\n\n## Real\n",
			want: []models.TOCItem{{Level: 2, ID: "real", Title: "Real"}},
		},
		{
			name: "frontmatter is not content",
			src:  "---\ntitle: \"Post\"\n---\n\n## Intro\n",
			want: []models.TOCItem{{Level: 2, ID: "intro", Title: "Intro"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := mustParse(t, tt.src)
			if !reflect.DeepEqual(b.TOC, tt.want) {
				t.Errorf("TOC = %+v, want %+v", b.TOC, tt.want)
			}
			assertTOCAnchors(t, b)
		})
	}
}

var headingTagRe = regexp.MustCompile(`<h([23]) id="`)

// fencedComments returns the "#" comment lines inside fenced code blocks.
func fencedComments(src []byte) []string {
	var comments []string
	inFence := false
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence && strings.HasPrefix(line, "#") {
			if c := strings.TrimSpace(strings.TrimLeft(line, "#")); c != "" {
				comments = append(comments, c)
			}
		}
	}
	return comments
}

func TestRealPostsTOC(t *testing.T) {
	totalComments := 0
	for name, src := range realPosts(t) {
		t.Run(name, func(t *testing.T) {
			b := mustParse(t, string(src))
			assertTOCAnchors(t, b)

			// A post without h2/h3 headings legitimately has an empty TOC.
			if got := len(headingTagRe.FindAllString(b.HTMLContent, -1)); got != len(b.TOC) {
				t.Errorf("TOC has %d entries, HTML has %d h2/h3 headings", len(b.TOC), got)
			}

			titles := make(map[string]bool, len(b.TOC))
			for _, item := range b.TOC {
				if item.Level != 2 && item.Level != 3 {
					t.Errorf("TOC entry %q has level %d", item.Title, item.Level)
				}
				titles[item.Title] = true
			}
			comments := fencedComments(src)
			totalComments += len(comments)
			for _, c := range comments {
				if titles[c] {
					t.Errorf("code comment %q appears in the TOC", c)
				}
			}
		})
	}
	if totalComments == 0 {
		t.Fatal("expected the posts to contain code comments; the regression check exercised nothing")
	}
}

// A pullquote is featured on the home page as a quotation from the post, so
// it must be text the post actually contains.
func TestRealPostsPullquotes(t *testing.T) {
	// Both went through the same typography (the body's Typographer and
	// finish, the pullquote's typeset); compare them with it undone, so a
	// quote is found even where the two set a character differently.
	plain := strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`, "–", "-", "—", "-", "…", "...", "\u00a0", " ", "--", "-")
	norm := func(s string) string { return strings.Join(strings.Fields(plain.Replace(s)), " ") }
	quoted := 0
	for name, src := range realPosts(t) {
		b := mustParse(t, string(src))
		if b.Pullquote == "" {
			continue
		}
		quoted++
		if !strings.Contains(norm(b.PlainText), norm(b.Pullquote)) {
			t.Errorf("%s: pullquote %q does not appear in the post", name, b.Pullquote)
		}
		if strings.ContainsAny(b.Pullquote, `'"`) || strings.Contains(b.Pullquote, "--") {
			t.Errorf("%s: pullquote %q keeps straight punctuation", name, b.Pullquote)
		}
	}
	if quoted == 0 {
		t.Error("no real post has a pullquote; the check exercised nothing")
	}
}

func TestCodeBlockMarkup(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		lang       string
		wantInPre  string
		highlights bool
	}{
		{
			name:       "labelled fence is highlighted",
			src:        "```go\nfunc main() {}\n```\n",
			lang:       "go",
			wantInPre:  `<span class="line"><span class="cl"><span class="kd">func</span>`,
			highlights: true,
		},
		{
			name:       "language label is lowercased",
			src:        "```Go\nfunc main() {}\n```\n",
			lang:       "go",
			wantInPre:  `<span class="kd">func</span>`,
			highlights: true,
		},
		{
			name:      "unlabelled fence is escaped plain text",
			src:       "```\nif a < b && c > d {}\n```\n",
			lang:      "text",
			wantInPre: "if a &lt; b &amp;&amp; c &gt; d {}\n</code></pre>",
		},
		{
			name:      "unknown language falls back to text",
			src:       "```nosuchlang\n<b>x</b>\n```\n",
			lang:      "text",
			wantInPre: "&lt;b&gt;x&lt;/b&gt;\n</code></pre>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := mustParse(t, tt.src).HTMLContent
			prefix := `<div class="code-block" data-lang="` + tt.lang + "\">\n" +
				strings.Replace(codeBlockHeader, "%s", tt.lang, 1) + "\n" + plainPreOpen
			if !strings.HasPrefix(html, prefix) {
				t.Fatalf("HTML does not start with the code block frame\n got: %q\nwant: %q", html, prefix)
			}
			if !strings.HasSuffix(html, "</code></pre>\n</div>\n") {
				t.Errorf("HTML does not end with the frame close: %q", html)
			}
			if !strings.Contains(html, tt.wantInPre) {
				t.Errorf("HTML missing %q:\n%s", tt.wantInPre, html)
			}
			if strings.Contains(html, "style=") {
				t.Errorf("HTML contains inline styles:\n%s", html)
			}
			if got := strings.Contains(html, `<span class="line">`); got != tt.highlights {
				t.Errorf("highlighted = %v, want %v", got, tt.highlights)
			}
		})
	}
}

type fakeCodeBlock struct {
	lang        []byte
	highlighted bool
}

func (f fakeCodeBlock) Language() ([]byte, bool)                     { return f.lang, f.lang != nil }
func (f fakeCodeBlock) Highlighted() bool                            { return f.highlighted }
func (f fakeCodeBlock) Attributes() highlighting.ImmutableAttributes { return nil }

func TestCodeBlockWrapperEscapesLanguage(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	ctx := fakeCodeBlock{lang: []byte(`x"><script>alert(1)</script>`), highlighted: true}
	codeBlockWrapper(w, ctx, true)
	codeBlockWrapper(w, ctx, false)
	_ = w.Flush()

	got := buf.String()
	if strings.Contains(got, "<script>") || strings.Contains(got, `x">`) {
		t.Fatalf("language label not escaped: %s", got)
	}
	if !strings.Contains(got, `data-lang="x&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`) {
		t.Errorf("unexpected data-lang escaping: %s", got)
	}
}

func assertWellFormedXML(t *testing.T, fragment string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader("<div>" + fragment + "</div>"))
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			t.Fatalf("HTML is not well-formed XML: %v", err)
		}
	}
}

func TestXHTMLOutput(t *testing.T) {
	src := "Text  \nwith a hard break and ![alt](a.png)\n\n---\n\n- [x] done\n- [ ] todo\n\n| a | b |\n|---|:-:|\n| 1 | 2 |\n\n```\nplain\n```\n\n```go\nx := 1\n```\n"
	html := mustParse(t, src).HTMLContent
	for _, want := range []string{"<br />", "<hr />", `<img src="a.png" alt="alt" />`, `type="checkbox" />`, `hidden=""`} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q:\n%s", want, html)
		}
	}
	assertWellFormedXML(t, html)

	for name, src := range realPosts(t) {
		t.Run(name, func(t *testing.T) {
			assertWellFormedXML(t, mustParse(t, string(src)).HTMLContent)
		})
	}
}

func TestPlainText(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		want     string
		wantCode string
	}{
		{
			name: "inline markup is dropped",
			src:  "## Hello `world`\n\nSome **bold** and _em_ text with a [link](https://x.y).\n",
			want: "Hello world\nSome bold and em text with a link.",
		},
		{
			name:     "code goes to CodeText, without fences",
			src:      "Intro\n\n```bash\n# make it   so\nsudo sysctl -w a=1 && echo '<ok>'\n```\n\n~~~\n**not bold**\n~~~\n\n    indented code\n\nOutro\n",
			want:     "Intro\nOutro",
			wantCode: "# make it so\nsudo sysctl -w a=1 && echo '<ok>'\n**not bold**\nindented code",
		},
		{
			name: "lists and quotes",
			src:  "- one\n- two\n  1. nested\n\n> quoted *text*\n",
			want: "one\ntwo\nnested\nquoted text",
		},
		{
			name: "entities and escapes are resolved",
			src:  "AT&amp;T \\*literal\\* &copy; 5 &lt; 6\n",
			want: "AT&T *literal* © 5 < 6",
		},
		{
			name: "images, autolinks and soft breaks",
			src:  "![diagram](d.png) see\n<https://example.com>\n",
			want: "diagram see https://example.com",
		},
		{
			name: "raw html is dropped",
			src:  "<div class=\"x\">block</div>\n\nkeep <b>bold</b> text\n",
			want: "keep bold text",
		},
		{
			name: "tables",
			src:  "| a | b |\n|---|---|\n| 1 | 2 |\n",
			want: "a\nb\n1\n2",
		},
		{
			name: "frontmatter is excluded",
			src:  "---\ntitle: \"T\"\ntags: [\"x\"]\n---\n\nBody\n",
			want: "Body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := mustParse(t, tt.src)
			if b.PlainText != tt.want {
				t.Errorf("PlainText = %q, want %q", b.PlainText, tt.want)
			}
			if b.CodeText != tt.wantCode {
				t.Errorf("CodeText = %q, want %q", b.CodeText, tt.wantCode)
			}
		})
	}
}

func TestRealPostsPlainText(t *testing.T) {
	syntax := regexp.MustCompile("```|~~~|\\*\\*|\\]\\(|(?m)^title:")
	for name, src := range realPosts(t) {
		t.Run(name, func(t *testing.T) {
			b := mustParse(t, string(src))
			if b.PlainText == "" {
				t.Fatal("empty plain text")
			}
			if m := syntax.FindString(b.PlainText); m != "" {
				t.Errorf("plain text contains markdown syntax %q", m)
			}
			lines := make(map[string]bool)
			for _, line := range strings.Split(b.PlainText, "\n") {
				lines[line] = true
			}
			for _, item := range b.TOC {
				if !lines[item.Title] {
					t.Errorf("plain text missing heading line %q", item.Title)
				}
				if marked := strings.Repeat("#", item.Level) + " " + item.Title; lines[marked] {
					t.Errorf("plain text keeps heading syntax %q", marked)
				}
			}
			// Code stays searchable: every "#" comment inside a fence is in
			// CodeText, and none is in the prose.
			comments := fencedComments(src)
			for _, c := range comments {
				if c = strings.Join(strings.Fields(c), " "); !strings.Contains(b.CodeText, c) {
					t.Errorf("code text missing %q", c)
				}
			}
			for _, line := range strings.Split(b.PlainText, "\n") {
				if strings.HasPrefix(line, "# ") {
					t.Errorf("plain text has the code comment %q", line)
				}
			}
		})
	}
}

func TestParseMetadata(t *testing.T) {
	src := "---\ntitle: \"Hello\"\ndescription: \"Desc\"\ndate: 2026-02-18\ntags: [\"go\", \" web \", \"go\", \"\"]\nstatus: draft\n---\n\n## Section\n\nfirst\n\n---\n\nafter the rule\n"
	b := mustParse(t, src)

	if b.Title != "Hello" || b.Description != "Desc" || b.Status != "draft" {
		t.Errorf("metadata = %q/%q/%q", b.Title, b.Description, b.Status)
	}
	if got := b.Date.Format("2006-01-02"); got != "2026-02-18" {
		t.Errorf("Date = %s", got)
	}
	if want := []string{"go", "web"}; !reflect.DeepEqual(b.Tags, want) {
		t.Errorf("Tags = %q, want %q", b.Tags, want)
	}
	if want := "\n## Section\n\nfirst\n\n---\n\nafter the rule\n"; b.Content != want {
		t.Errorf("Content = %q, want raw markdown body %q", b.Content, want)
	}
	if b.ReadingTime != 1 {
		t.Errorf("ReadingTime = %d", b.ReadingTime)
	}
}

func TestParseInvalidFrontmatter(t *testing.T) {
	if _, err := Parse([]byte("---\ntitle: [unclosed\n---\n\nbody\n")); err == nil {
		t.Fatal("expected an error for invalid YAML frontmatter")
	}
}
