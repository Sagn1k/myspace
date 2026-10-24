package blog

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sagnikb/myspace/internal/models"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/extension"
)

// typographic holds every character the Typographer writes.
const typographic = "‘’“”–—…«»"

// typographicEntity matches the named entities goldmark's Typographer writes
// by default; none of them may appear anywhere in a post's output.
var typographicEntity = regexp.MustCompile(`&(lsquo|rsquo|ldquo|rdquo|ndash|mdash|hellip|laquo|raquo);`)

// codeRe matches rendered code: blocks (chroma's and plain <pre>) and spans.
var codeRe = regexp.MustCompile(`(?s)<pre[^>]*>.*?</pre>|<code>.*?</code>`)

func TestTypographySubstitutions(t *testing.T) {
	for p := extension.LeftSingleQuote; p <= extension.Apostrophe; p++ {
		v := typography[p]
		if utf8.RuneCountInString(v) != 1 || !strings.ContainsAny(v, typographic) {
			t.Errorf("punctuation %d is replaced with %q, want one typographic character", p, v)
		}
	}
}

func TestTypographer(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"apostrophes", "It's Sagnik's site, isn't it? The '90s.\n", "<p>It’s Sagnik’s site, isn’t it? The ’90s.</p>\n"},
		{"double quotes", "He said \"retry\" twice.\n", "<p>He said “retry” twice.</p>\n"},
		{"single quotes", "A 'quoted' word.\n", "<p>A ‘quoted’ word.</p>\n"},
		{"dashes and ellipsis", "a -- b --- c...\n", "<p>a\u00a0– b\u00a0— c…</p>\n"},
		{"typed dashes bind to the word before", "under the hood — namespaces, a – b\n", "<p>under the hood\u00a0— namespaces, a\u00a0– b</p>\n"},
		{"unspaced dashes are left alone", "a—b, a --- b's end, 1990---2000\n", "<p>a—b, a\u00a0— b’s end, 1990—2000</p>\n"},
		{"numeric ranges", "Set it to 50-75% (100-200 here), 1.1-1.5 or 10-50x.\n", "<p>Set it to 50–75% (100–200 here), 1.1–1.5 or 10–50x.</p>\n"},
		{"not ranges", "x86-64, v1.2-3, 2026-01-15, 2026-01, S3-compatible, 978-3-16, UTF-8\n", "<p>x86-64, v1.2-3, 2026-01-15, 2026-01, S3-compatible, 978-3-16, UTF-8</p>\n"},
		{"ranges inside markup", "**5-10** and [30-60](https://x.y/30-60) MB\n", "<p><strong>5–10</strong> and <a href=\"https://x.y/30-60\">30–60</a> MB</p>\n"},
		{"code spans are never finished", "`5-10 — x` and `a -- b`\n", "<p><code>5-10 — x</code> and <code>a -- b</code></p>\n"},
		{"range at a paragraph start", "5-10 MB\n\na 5-10\n", "<p>5–10 MB</p>\n<p>a 5–10</p>\n"},
		{"soft line breaks survive", "5-10\nmore -- text\n", "<p>5–10\nmore\u00a0– text</p>\n"},
		{"angle quotes", "<<guillemets>>\n", "<p>«guillemets»</p>\n"},
		{"code span is untouched", "Run `ls --all \"it's\" ... <<EOF` now.\n", "<p>Run <code>ls --all &quot;it's&quot; ... &lt;&lt;EOF</code> now.</p>\n"},
		{"link destination is untouched", "[it's](https://x.y/a--b...c)\n", "<p><a href=\"https://x.y/a--b...c\">it’s</a></p>\n"},
		{"autolink is untouched", "<https://x.y/a--b...c>\n", "<p><a href=\"https://x.y/a--b...c\">https://x.y/a--b...c</a></p>\n"},
		{"raw html is untouched", "<span title=\"it's -- ...\">x</span>\n", "<p><span title=\"it's -- ...\">x</span></p>\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustParse(t, tt.src).HTMLContent; got != tt.want {
				t.Errorf("HTML = %q, want %q", got, tt.want)
			}
		})
	}
}

// Code blocks render exactly as they would without the Typographer.
func TestTypographerLeavesCodeBlocks(t *testing.T) {
	src := "```bash\necho \"it's\" -- --- ... <<EOF 5-10 a -- b\n```\n\n```\nplain \"it's\" -- --- ... >> 5-10 a -- b\n```\n\n~~~go\ns := \"it's -- ... 5-10\"\n~~~\n\n    indented \"it's\" -- ... 5-10 a -- b\n"
	got := mustParse(t, src).HTMLContent
	var want bytes.Buffer
	if err := newMarkdown().Convert([]byte(src), &want); err != nil {
		t.Fatal(err)
	}
	if got != want.String() {
		t.Errorf("code blocks changed:\n got: %s\nwant: %s", got, want.String())
	}
	if strings.ContainsAny(got, typographic+"\u00a0") {
		t.Errorf("code blocks contain typographic characters: %s", got)
	}
}

func TestTypographyInTOCAndPlainText(t *testing.T) {
	b := mustParse(t, "## Don't \"panic\"...\n\nIt's -- fine.\n\n```\nit's -- \"raw\"...\n```\n")
	// The heading ID comes from the source line, so typography never moves an anchor.
	wantTOC := []models.TOCItem{{Level: 2, ID: "dont-panic", Title: "Don’t “panic”…"}}
	if !reflect.DeepEqual(b.TOC, wantTOC) {
		t.Errorf("TOC = %+v, want %+v", b.TOC, wantTOC)
	}
	assertTOCAnchors(t, b)
	if want := "Don’t “panic”…\nIt’s\u00a0– fine."; b.PlainText != want {
		t.Errorf("PlainText = %q, want %q", b.PlainText, want)
	}
	if want := "it's -- \"raw\"..."; b.CodeText != want {
		t.Errorf("CodeText = %q, want %q", b.CodeText, want)
	}
}

func TestRealPostsTypography(t *testing.T) {
	plain := newMarkdown(meta.Meta)
	curly := 0
	for name, src := range realPosts(t) {
		t.Run(name, func(t *testing.T) {
			b := mustParse(t, string(src))
			var untyped bytes.Buffer
			if err := plain.Convert(src, &untyped); err != nil {
				t.Fatal(err)
			}

			// Every code span and block is byte-identical to the output
			// without the Typographer.
			got, want := codeRe.FindAllString(b.HTMLContent, -1), codeRe.FindAllString(untyped.String(), -1)
			if len(want) == 0 {
				t.Fatal("post has no code; the check exercised nothing")
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("code changed by the Typographer (%d vs %d segments)", len(got), len(want))
				for i := range min(len(got), len(want)) {
					if got[i] != want[i] {
						t.Errorf("first difference:\n got: %s\nwant: %s", got[i], want[i])
						break
					}
				}
			}

			for field, text := range map[string]string{"HTMLContent": b.HTMLContent, "PlainText": b.PlainText} {
				if m := typographicEntity.FindString(text); m != "" {
					t.Errorf("%s contains the entity %s", field, m)
				}
			}
			if strings.Contains(b.HTMLContent, "’") && strings.Contains(b.PlainText, "’") {
				curly++
			}

			// TOC titles keep no straight quotes or ASCII dashes for the
			// Typographer to have missed.
			for _, item := range b.TOC {
				if strings.ContainsAny(item.Title, `'"&`) || strings.Contains(item.Title, "--") || strings.Contains(item.Title, "...") {
					t.Errorf("TOC title %q has unconverted punctuation or entity text", item.Title)
				}
			}
		})
	}
	if curly == 0 {
		t.Error("no real post has a typographic apostrophe in its HTML and plain text; is the Typographer enabled?")
	}
}

func TestTypeset(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"plain", "Building CLI Tools in Go", "Building CLI Tools in Go"},
		{"apostrophe in a word", "using Go's concurrency primitives", "using Go’s concurrency primitives"},
		{"contractions", "Don't panic, it isn't, you're, I'll", "Don’t panic, it isn’t, you’re, I’ll"},
		{"digit then letter", "the 90's", "the 90’s"},
		{"decade", "back in the '90s", "back in the ’90s"},
		{"double quotes", `He said "retry" twice`, "He said “retry” twice"},
		{"single quotes", "A 'quoted' word", "A ‘quoted’ word"},
		{"quotes around an apostrophe", `"Don't panic"`, "“Don’t panic”"},
		{"quotes in parentheses", `("quoted")`, "(“quoted”)"},
		{"plural possessive", "the Smiths' house", "the Smiths’ house"},
		{"unpaired double quote stays", `a 21" screen`, `a 21" screen`},
		{"unpaired opening quote stays", `say "hello`, `say "hello`},
		{"dashes and ellipsis", "a -- b --- c... d", "a\u00a0– b\u00a0— c… d"},
		{"spaced em dash", "under the hood — network namespaces", "under the hood\u00a0— network namespaces"},
		{"leading dash", "-- leading", "– leading"},
		{"ranges", "50-75% of RAM, 1.1-1.5, 9:00-17:00", "50–75% of RAM, 1.1–1.5, 9:00–17:00"},
		{"dates and names", "2026-01-15 and x86-64", "2026-01-15 and x86-64"},
		{"already typeset", "already “curly” and Go’s — fine", "already “curly” and Go’s\u00a0— fine"},
		{"markup characters are text", "C# & <b>F#</b> *x*", "C# & <b>F#</b> *x*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := typeset(tt.in); got != tt.want {
				t.Errorf("typeset(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Front matter gets the body's punctuation at parse time.
func TestFrontMatterTypography(t *testing.T) {
	b := mustParse(t, "---\ntitle: \"Go's \\\"quiet\\\" -- part 2\"\ndescription: \"It's 'fast'... under the hood — 5-10x\"\npullquote: \"Don't retry -- ever.\"\n---\n\nDon't retry -- ever.\n")
	if want := "Go’s “quiet”\u00a0– part 2"; b.Title != want {
		t.Errorf("Title = %q, want %q", b.Title, want)
	}
	if want := "It’s ‘fast’… under the hood\u00a0— 5–10x"; b.Description != want {
		t.Errorf("Description = %q, want %q", b.Description, want)
	}
	if want := "Don’t retry\u00a0– ever."; b.Pullquote != want {
		t.Errorf("Pullquote = %q, want %q", b.Pullquote, want)
	}
	// the body sets the same sentence the same way
	if !strings.Contains(b.HTMLContent, b.Pullquote) {
		t.Errorf("body %q does not contain the pullquote %q", b.HTMLContent, b.Pullquote)
	}
}

func TestRealPostsFrontMatterTypography(t *testing.T) {
	apostrophes := 0
	for name, src := range realPosts(t) {
		b := mustParse(t, string(src))
		for field, v := range map[string]string{"title": b.Title, "description": b.Description, "pullquote": b.Pullquote} {
			if strings.ContainsAny(v, `'"`) || strings.Contains(v, "--") || strings.Contains(v, "...") {
				t.Errorf("%s: %s %q keeps straight punctuation", name, field, v)
			}
			if strings.Contains(v, " \u2014") || strings.Contains(v, " \u2013 ") {
				t.Errorf("%s: %s %q has a breakable space before a dash", name, field, v)
			}
			apostrophes += strings.Count(v, "’")
		}
	}
	if apostrophes == 0 {
		t.Error("no real title, description or pullquote has an apostrophe; the check exercised nothing")
	}
}

// Document parses a body exactly as Parse renders it.
func TestDocument(t *testing.T) {
	src := "---\ntitle: \"T\"\n---\n\n## It's 5-10\n\nText — more.\n"
	b := mustParse(t, src)
	doc, source := Document(b.Content)
	if got := extractTOC(doc, source); !reflect.DeepEqual(got, b.TOC) {
		t.Errorf("Document TOC = %+v, want %+v", got, b.TOC)
	}
	if got := plainText(doc, source); got != b.PlainText {
		t.Errorf("Document text = %q, want %q", got, b.PlainText)
	}
	// a body that starts with a rule is not front matter
	doc, source = Document("---\n\nAfter the rule.\n")
	if got := plainText(doc, source); got != "After the rule." {
		t.Errorf("Document text = %q, want the paragraph after the rule", got)
	}
}
