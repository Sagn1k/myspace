package download

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sagnikb/myspace/internal/blog"
	"github.com/sagnikb/myspace/internal/models"
)

// testPosts returns every real post plus one with XML-hostile metadata.
func testPosts(t *testing.T) map[string]*models.Blog {
	t.Helper()
	paths, err := filepath.Glob("../../content/blogs/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no posts found in content/blogs")
	}
	posts := make(map[string]*models.Blog)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		b, err := blog.Parse(data)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		b.Slug = strings.TrimSuffix(filepath.Base(p), ".md")
		posts[b.Slug] = b
	}

	hostile, err := blog.Parse([]byte("---\ntitle: \"Tom & Jerry <3 \\\"quotes\\\"\"\ndescription: \"a < b && c\"\ndate: 2026-01-01\ntags: [\"c&c\"]\n---\n\n## Section &amp; more\n\nLine  \nbreak, rule:\n\n---\n\n![img](a.png) &copy; &nbsp;\n\n```\n<raw> & code\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	hostile.Slug = "hostile"
	hostile.Date = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	posts[hostile.Slug] = hostile
	return posts
}

func checkWellFormed(t *testing.T, name string, data []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			t.Errorf("%s is not well-formed XML: %v", name, err)
			return
		}
	}
}

func TestGenerateEPUB(t *testing.T) {
	for slug, post := range testPosts(t) {
		t.Run(slug, func(t *testing.T) {
			data, err := GenerateEPUB(post)
			if err != nil {
				t.Fatal(err)
			}
			zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}

			first := zr.File[0]
			if first.Name != "mimetype" || first.Method != zip.Store {
				t.Errorf("first entry = %s (method %d), want stored mimetype", first.Name, first.Method)
			}

			xmlFiles := 0
			for _, f := range zr.File {
				rc, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				content, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					t.Fatal(err)
				}
				switch filepath.Ext(f.Name) {
				case ".xml", ".opf", ".ncx", ".xhtml":
					xmlFiles++
					checkWellFormed(t, f.Name, content)
				case "":
					if string(content) != "application/epub+zip" {
						t.Errorf("mimetype = %q", content)
					}
				}
			}
			if xmlFiles != 5 {
				t.Errorf("checked %d XML files, want 5", xmlFiles)
			}
		})
	}
}

// namedEntityRe matches named character references. XHTML (an EPUB chapter)
// only defines the five XML ones.
var namedEntityRe = regexp.MustCompile(`&[A-Za-z][A-Za-z0-9]*;`)

// The Typographer's curly quotes and dashes reach EPUB chapters as UTF-8
// characters: named HTML entities such as &rsquo; are undefined in XHTML.
func TestEPUBTypography(t *testing.T) {
	curly := 0
	for slug, post := range testPosts(t) {
		chapter := chapterXHTML(post)
		for _, m := range namedEntityRe.FindAllString(chapter, -1) {
			switch m {
			case "&amp;", "&lt;", "&gt;", "&quot;", "&apos;":
			default:
				t.Errorf("%s: chapter contains the entity %s, which XHTML does not define", slug, m)
			}
		}
		checkWellFormed(t, slug+"/chapter1.xhtml", []byte(chapter))
		if strings.Contains(chapter, "\u2019") {
			curly++
		}
	}
	if curly == 0 {
		t.Error("no chapter contains a typographic apostrophe; is the Typographer enabled?")
	}
}

// textOperands returns the strings an uncompressed PDF's text operators
// draw ("(...) Tj"), with the PDF string escapes undone.
func textOperands(pdf []byte) []string {
	var ops []string
	for _, m := range tjRe.FindAllSubmatch(pdf, -1) {
		var b []byte
		for i := 0; i < len(m[1]); i++ {
			c := m[1][i]
			if c == '\\' && i+1 < len(m[1]) {
				i++
				switch c = m[1][i]; c {
				case 'n':
					c = '\n'
				case 'r':
					c = '\r'
				case 't':
					c = '\t'
				}
			}
			b = append(b, c)
		}
		ops = append(ops, string(b))
	}
	return ops
}

var tjRe = regexp.MustCompile(`\(((?:\\.|[^\\()])*)\)\s*Tj`)

// uncompressedPDF typesets the post the way GeneratePDF does, without
// compressing the page contents.
func uncompressedPDF(t *testing.T, post *models.Blog) []byte {
	t.Helper()
	data, err := generatePDF(post, models.DefaultConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("output is not a PDF: %q", data[:min(len(data), 16)])
	}
	if bytes.Contains(data, []byte("/FlateDecode")) {
		t.Fatal("page contents are compressed")
	}
	return data
}

func hasOperand(ops []string, want string) bool {
	for _, op := range ops {
		if op == want {
			return true
		}
	}
	return false
}

// The core fonts draw cp1252: every string a post's PDF draws is cp1252, so
// the Typographer's dashes, quotes and the list bullets are single cp1252
// bytes and no UTF-8 sequence (0xE2 0x80 …) reaches a text operator.
func TestPDFTextIsCP1252(t *testing.T) {
	for slug, post := range testPosts(t) {
		t.Run(slug, func(t *testing.T) {
			ops := textOperands(uncompressedPDF(t, post))
			if len(ops) == 0 {
				t.Fatal("no text operators found")
			}
			for _, op := range ops {
				if strings.Contains(op, "\xE2\x80") || strings.Contains(op, "\xC2\xA0") || strings.Contains(op, "\xEF\xBF\xBD") {
					t.Errorf("text operator draws UTF-8 bytes: %q", op)
				}
			}
		})
	}

	ops := textOperands(uncompressedPDF(t, testPosts(t)["designing-distributed-scrapers"]))
	all := strings.Join(ops, "\n")
	for name, b := range map[string]string{
		"em dash": "\x97", "bullet": "\x95", "right single quote": "\x92",
		"no-break space": "\xA0", "section sign": "\xA7", "middle dot": "\xB7",
	} {
		if !strings.Contains(all, b) {
			t.Errorf("no %s (cp1252 %q) in the scrapers PDF", name, b)
		}
	}
}

// Code keeps every character and its indentation, in its own cells; the
// prose loses its markdown syntax.
func TestPDFContent(t *testing.T) {
	posts := testPosts(t)
	for slug, want := range map[string][]string{
		"designing-distributed-scrapers": {
			"func (w *Worker) Run(ctx context.Context) error {",
			"    MaxDepth  int               `json:" + `"max_depth"` + "`",
			"Listing 01",
			"Scheduler",
		},
		"understanding-container-networking": {"# Create a veth pair"},
		"building-cli-tools-in-go":           {"      - -X main.version={{.Version}}", "1."},
	} {
		ops := textOperands(uncompressedPDF(t, posts[slug]))
		for _, w := range want {
			if !hasOperand(ops, w) {
				t.Errorf("%s: no text operator draws %q", slug, w)
			}
		}
		for _, op := range ops {
			if strings.Contains(op, "**") {
				t.Errorf("%s: markdown emphasis markers in %q", slug, op)
			}
		}
	}
}

// Every kind of markdown block and inline the site renders has a place in
// the PDF.
func TestPDFMarkdown(t *testing.T) {
	src := "---\ntitle: Coverage\ndate: 2026-01-02\n---\n\n" +
		"# A first-level heading\n\n" +
		"Text with **bold**, *italic*, ***both***, `code`, ~~struck~~, a [relative link](docs/intro), " +
		"an [anchor](#a-table), <https://example.com/auto> and ![diagram of the flow](flow.png).  \n" +
		"After a hard break: emoji ROCKET, arrows ARROW LE and Łódź.\n\n" +
		"> A quoted line\n> that continues.\n\n" +
		"3. three\n4. four\n   - nested\n   - [x] done\n   - [ ] open\n\n" +
		"---\n\n" +
		"## A table\n\n" +
		"| Name | Count | Note |\n|:-----|------:|:----:|\n| a | 1 | first |\n" +
		"| b | 22 | second, a much longer cell that has to wrap inside its column at least once or twice over |\n\n" +
		"#### Fourth level\n\n" +
		"<div>raw html block</div>\n\n" +
		"```go\nfunc main() { fmt.Println(\x22a line that is far too long to fit in the listing box, so it wraps onto another row\x22) }\n\tindented()\n```\n\n" +
		strings.Repeat("unbreakable", 20) + "\n"
	src = strings.NewReplacer("ROCKET", string(rune(0x1F680)), "ARROW", "\u2192", "LE", "\u2264").Replace(src)
	post, err := blog.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	post.Slug = "coverage"
	data := uncompressedPDF(t, post)
	ops := textOperands(data)
	all := strings.Join(ops, "\n")
	for _, want := range []string{
		"A first-level heading", "bold", "italic", "both", "code", "struck",
		"[Image:", "diagram of the flow]", "emoji , arrows -> <= and L\xf3dz.",
		"A quoted line that continues.", "3.", "4.", "[x] ", "[ ] ", "nested",
		"\xa7 01", "A table", "Name", "Count", "22", "Fourth level",
		"Listing 01", "func main() { fmt.Println(\x22a line that is far too long to fit in the listing box,",
		"    indented()",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the PDF does not draw %q", want)
		}
	}
	if strings.Contains(all, "raw html block") {
		t.Error("the PDF draws a raw HTML block")
	}
	if strings.Contains(all, strings.Repeat("unbreakable", 20)) {
		t.Error("a word wider than the page was not cut")
	}
	// the over-long code line continues on a row indented past it
	continued := false
	for _, op := range ops {
		if strings.HasPrefix(op, "    ") && strings.Contains(op, "row") {
			continued = true
		}
	}
	if !continued {
		t.Error("the long code line did not wrap onto an indented row")
	}
	for _, want := range []string{
		"/URI (https://sagnikbhowmick.com/blog/docs/intro)", // relative to the post, as on the page
		"/URI (https://example.com/auto)",
		"/Outlines",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("the PDF has no %s", want)
		}
	}
}

func TestASCP1252(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain ASCII", "plain ASCII"},
		{"Go’s “quoted” — dash – … • € ™", "Go’s “quoted” — dash – … • € ™"},
		{"café ×2 ½ §", "café ×2 ½ §"},
		{"a \u2192 b \u2264 c \u2260 d", "a -> b <= c != d"},
		{"Łódź, Dvořák, Kraków", "Lódz, Dvorák, Kraków"},
		{"e\u0301", "e"},
		{"ship it " + string(rune(0x1F680)), "ship it "},
		{"zero\u200bwidth", "zerowidth"},
		{"soft\u00adhyphen", "softhyphen"},
		{"narrow\u202fspace", "narrow\u00a0space"},
		{"\u251c\u2500\u2500 cmd/", "|-- cmd/"},
		{"\u65e5\u672c", "??"},
	} {
		if got := asCP1252(c.in); got != c.want {
			t.Errorf("asCP1252(%q) = %q, want %q", c.in, got, c.want)
		}
		for _, r := range asCP1252(c.in) {
			if !inCP1252(r) {
				t.Errorf("asCP1252(%q) keeps %q, which cp1252 lacks", c.in, r)
			}
		}
	}
}

func TestWrapCode(t *testing.T) {
	for _, c := range []struct {
		line string
		cols int
		want []string
	}{
		{"short", 20, []string{"short"}},
		{"aaaa bbbb cccc dddd", 10, []string{"aaaa bbbb", "    cccc", "    dddd"}},
		{"  x = call(argument, another)", 16, []string{"  x = call(", "      argument,", "      another)"}},
		{"abcdefghijklmnop", 8, []string{"abcdefgh", "    ijkl", "    mnop"}},
	} {
		if got := wrapCode(c.line, c.cols); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("wrapCode(%q, %d) = %q, want %q", c.line, c.cols, got, c.want)
		}
	}
	if got := expandTabs("\ta\tb"); got != "    a   b" {
		t.Errorf("expandTabs = %q", got)
	}
}

// An EPUB 3 has exactly one navigation document, listing the article and
// its sections; each entry points at a heading the chapter has.
func TestEPUBNav(t *testing.T) {
	posts := testPosts(t)
	post := posts["designing-distributed-scrapers"]
	opf := contentOPF(post)
	if n := strings.Count(opf, `properties="nav"`); n != 1 {
		t.Fatalf("content.opf declares %d nav documents, want 1", n)
	}
	if !strings.Contains(opf, `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`) {
		t.Errorf("content.opf does not declare nav.xhtml:\n%s", opf)
	}
	for slug, p := range posts {
		nav := navXHTML(p)
		checkWellFormed(t, slug+"/nav.xhtml", []byte(nav))
		if !strings.Contains(nav, `<nav epub:type="toc" id="toc">`) {
			t.Errorf("%s: nav.xhtml has no toc nav", slug)
		}
		chapter := chapterXHTML(p)
		for _, it := range p.TOC {
			if !strings.Contains(nav, `href="chapter1.xhtml#`+it.ID+`"`) {
				t.Errorf("%s: nav.xhtml does not list %q", slug, it.Title)
			}
			if !strings.Contains(chapter, ` id="`+it.ID+`"`) {
				t.Errorf("%s: the chapter has no heading #%s", slug, it.ID)
			}
		}
		if strings.Contains(nav, "<ol>\n        </ol>") || strings.Contains(nav, "<ol></ol>") {
			t.Errorf("%s: nav.xhtml has an empty list", slug)
		}
	}
	// h3 entries nest under the h2 before them
	nested := navXHTML(&models.Blog{Title: "T", TOC: []models.TOCItem{
		{Level: 2, ID: "a", Title: "A"}, {Level: 3, ID: "a1", Title: "A1"}, {Level: 3, ID: "a2", Title: "A2"},
		{Level: 2, ID: "b", Title: "B"},
	}})
	checkWellFormed(t, "nested nav.xhtml", []byte(nested))
	flat := strings.Join(strings.Fields(nested), "")
	want := `<li><ahref="chapter1.xhtml">T</a><ol><li><ahref="chapter1.xhtml#a">A</a><ol><li><ahref="chapter1.xhtml#a1">A1</a></li><li><ahref="chapter1.xhtml#a2">A2</a></li></ol></li><li><ahref="chapter1.xhtml#b">B</a></li></ol></li>`
	if !strings.Contains(flat, want) {
		t.Errorf("nested nav = %s\nwant it to contain %s", flat, want)
	}
}

func TestGeneratePDF(t *testing.T) {
	for slug, post := range testPosts(t) {
		t.Run(slug, func(t *testing.T) {
			data, err := GeneratePDF(post, models.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(data, []byte("%PDF-")) {
				t.Errorf("output is not a PDF: %q", data[:min(len(data), 16)])
			}
		})
	}
}
