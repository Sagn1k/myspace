package handlers

// Rendering checks for the article page (blog_post.html) and the error page
// (error.html) with the real templates. They cover what a browser check of
// the live site cannot reach: 5xx error pages, a site with fewer than four
// posts, and posts without tags, a description or headings.

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/sagnikb/myspace/internal/models"
)

// htmlNode is a minimal document tree: enough to assert on structure,
// attributes and text without adding an HTML parser dependency.
type htmlNode struct {
	tag    string // "" for a text node
	attr   map[string]string
	text   string
	kids   []*htmlNode
	parent *htmlNode
}

var scriptRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)

// parseHTML builds the tree with encoding/xml in its lenient HTML mode
// (void elements, valueless attributes, HTML entities). Script bodies are
// not markup, so they are removed first.
func parseHTML(t *testing.T, body string) *htmlNode {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(scriptRe.ReplaceAllString(body, "")))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	root := &htmlNode{tag: "#document", attr: map[string]string{}}
	cur := root
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return root
		}
		if err != nil {
			t.Fatalf("parse HTML: %v", err)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			n := &htmlNode{tag: strings.ToLower(tok.Name.Local), attr: map[string]string{}, parent: cur}
			for _, a := range tok.Attr {
				n.attr[strings.ToLower(a.Name.Local)] = a.Value
			}
			cur.kids = append(cur.kids, n)
			cur = n
		case xml.EndElement:
			name := strings.ToLower(tok.Name.Local)
			for n := cur; n != root; n = n.parent {
				if n.tag == name {
					cur = n.parent
					break
				}
			}
		case xml.CharData:
			cur.kids = append(cur.kids, &htmlNode{text: string(tok), parent: cur})
		}
	}
}

type matcher func(*htmlNode) bool

func tagIs(name string) matcher { return func(n *htmlNode) bool { return n.tag == name } }
func classIs(c string) matcher  { return func(n *htmlNode) bool { return n.hasClass(c) } }
func attrIs(k, v string) matcher {
	return func(n *htmlNode) bool { val, ok := n.attr[k]; return ok && val == v }
}
func hasAttr(k string) matcher { return func(n *htmlNode) bool { _, ok := n.attr[k]; return ok } }
func all(ms ...matcher) matcher {
	return func(n *htmlNode) bool {
		for _, m := range ms {
			if !m(n) {
				return false
			}
		}
		return true
	}
}

func (n *htmlNode) hasClass(c string) bool {
	return slices.Contains(strings.Fields(n.attr["class"]), c)
}

// find returns the elements below n that match, in document order.
func (n *htmlNode) find(m matcher) []*htmlNode {
	var out []*htmlNode
	var walk func(*htmlNode)
	walk = func(p *htmlNode) {
		for _, k := range p.kids {
			if k.tag != "" && m(k) {
				out = append(out, k)
			}
			walk(k)
		}
	}
	walk(n)
	return out
}

// one returns the only element below n that matches and fails the test
// when there is not exactly one.
func (n *htmlNode) one(t *testing.T, what string, m matcher) *htmlNode {
	t.Helper()
	found := n.find(m)
	if len(found) != 1 {
		t.Fatalf("%s: found %d, want 1", what, len(found))
	}
	return found[0]
}

// children returns n's child elements that match.
func (n *htmlNode) children(m matcher) []*htmlNode {
	var out []*htmlNode
	for _, k := range n.kids {
		if k.tag != "" && m(k) {
			out = append(out, k)
		}
	}
	return out
}

func (n *htmlNode) within(ancestor *htmlNode) bool {
	for p := n.parent; p != nil; p = p.parent {
		if p == ancestor {
			return true
		}
	}
	return false
}

// textContent is the element's text with whitespace runs (en spaces too)
// collapsed to single spaces.
func (n *htmlNode) textContent() string {
	var b strings.Builder
	var walk func(*htmlNode)
	walk = func(p *htmlNode) {
		if p.tag == "" {
			b.WriteString(p.text)
			return
		}
		for _, k := range p.kids {
			walk(k)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// collapse is s with its whitespace collapsed the way textContent collapses
// the page's (a no-break space counts as whitespace there), for comparing
// text from the data with text from the page.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func texts(nodes []*htmlNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.textContent()
	}
	return out
}

// request sends method target (used verbatim as the request URI).
func request(t *testing.T, app *fiber.App, method, target string) response {
	t.Helper()
	req := httptest.NewRequest(method, "/", nil)
	req.RequestURI = target
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func pageTitle(t *testing.T, doc *htmlNode) string {
	t.Helper()
	return doc.one(t, "<title>", tagIs("title")).textContent()
}

// headingLevels lists the h1–h6 levels outside the prose (the template's own
// headings), in document order.
func headingLevels(doc *htmlNode) []int {
	var levels []int
	for _, h := range doc.find(func(n *htmlNode) bool {
		return len(n.tag) == 2 && n.tag[0] == 'h' && n.tag[1] >= '1' && n.tag[1] <= '6'
	}) {
		inProse := false
		for p := h.parent; p != nil; p = p.parent {
			if p.hasClass("prose") {
				inProse = true
			}
		}
		if !inProse {
			levels = append(levels, int(h.tag[1]-'0'))
		}
	}
	return levels
}

func checkNoSkippedLevels(t *testing.T, doc *htmlNode) {
	t.Helper()
	prev := 0
	for _, l := range headingLevels(doc) {
		if l > prev+1 {
			t.Errorf("heading levels %v skip from h%d to h%d", headingLevels(doc), prev, l)
			return
		}
		prev = l
	}
}

// checkPost asserts everything blog_post.html promises for post, given the
// Keep reading items the handler computes for it.
func checkPost(t *testing.T, body string, post *models.Blog, keep []models.KeepItem, config models.SiteConfig) {
	t.Helper()
	if !strings.Contains(body, post.HTMLContent) {
		t.Error("the prose is not the post's HTMLContent verbatim")
	}
	doc := parseHTML(t, body)

	main := doc.one(t, "main#main", attrIs("id", "main"))
	if c, ok := main.attr["class"]; ok {
		t.Errorf(`main has class=%q; a page without "main-class" gets no class attribute`, c)
	}
	if want := collapse(post.Title + " · " + config.Title); pageTitle(t, doc) != want {
		t.Errorf("<title> = %q, want %q", pageTitle(t, doc), want)
	}

	// The reading-progress bar is gone: no page fills header-extra now.
	if bars := doc.find(classIs("progress")); len(bars) != 0 {
		t.Errorf("%d .progress elements; the reading-progress bar was removed", len(bars))
	}

	// Head: the title, the dek and one meta line.
	h1 := doc.one(t, "h1", tagIs("h1"))
	if h1.attr["id"] != "post-title" || h1.textContent() != collapse(post.Title) {
		t.Errorf("h1 = #%s %q, want #post-title %q", h1.attr["id"], h1.textContent(), post.Title)
	}
	if long := utf8.RuneCountInString(post.Title) > 70; h1.hasClass("article-title--long") != long {
		t.Errorf("article-title--long = %v for a %d-character title", !long, utf8.RuneCountInString(post.Title))
	}
	article := doc.one(t, "article[aria-labelledby=post-title]", all(tagIs("article"), attrIs("aria-labelledby", "post-title")))
	if !h1.within(article) {
		t.Error("the h1 is outside the article it labels")
	}
	head := doc.one(t, "header.article-head", classIs("article-head"))
	if n := len(head.find(classIs("crumbs"))); n != 0 {
		t.Errorf("%d breadcrumbs; the nav's current “Writing” and the date replace them", n)
	}
	if n, m := len(doc.find(classIs("label"))), len(doc.find(classIs("label--accent"))); n != 0 || m != 0 {
		t.Errorf("%d .label and %d .label--accent elements; no micro-labels remain", n, m)
	}
	dek := head.find(classIs("article-dek"))
	if post.Description == "" {
		if len(dek) != 0 {
			t.Error("dek on a post without a description")
		}
	} else if len(dek) != 1 || dek[0].textContent() != collapse(post.Description) {
		t.Errorf("dek = %q, want the description", texts(dek))
	}
	meta := head.one(t, ".article-meta", classIs("article-meta"))
	tm := meta.one(t, "post date", tagIs("time"))
	date := post.Date.Format("2 January 2006")
	if tm.attr["datetime"] != post.Date.Format("2006-01-02") || tm.textContent() != date {
		t.Errorf("post date = %s %q", tm.attr["datetime"], tm.textContent())
	}
	if want := date + " · " + strconv.Itoa(post.ReadingTime) + " min read"; meta.textContent() != want {
		t.Errorf("article meta = %q, want %q", meta.textContent(), want)
	}

	// Downloads: one text line in the end matter; "Copy link" was removed.
	end := doc.one(t, "footer.article-end", all(tagIs("footer"), classIs("article-end")))
	files := doc.one(t, ".article-files", classIs("article-files"))
	if !files.within(end) {
		t.Error("the downloads line is outside footer.article-end")
	}
	var hrefs []string
	for _, a := range files.find(tagIs("a")) {
		hrefs = append(hrefs, a.attr["href"])
	}
	if want := []string{"/blog/" + post.Slug + "/download/pdf", "/blog/" + post.Slug + "/download/epub"}; !slices.Equal(hrefs, want) {
		t.Errorf("download links = %q, want %q", hrefs, want)
	}
	if got := files.textContent(); got != "Download PDF or EPUB" {
		t.Errorf("downloads line = %q, want “Download PDF or EPUB”", got)
	}
	// Each link's name says what it does on its own (a screen reader's
	// links list), and contains its visible label.
	var names []string
	for _, a := range files.find(tagIs("a")) {
		names = append(names, a.attr["aria-label"])
	}
	if want := []string{"Download PDF", "Download EPUB"}; !slices.Equal(names, want) {
		t.Errorf("download link names = %q, want %q", names, want)
	}
	if n := len(doc.find(hasAttr("data-copy-url"))); n != 0 {
		t.Errorf("%d copy-link controls; Copy link was removed", n)
	}

	// Contents: one closed <details> listing exactly the prose's top-level
	// h2/h3 headings, in order.
	prose := main.one(t, "div.prose", all(tagIs("div"), classIs("prose")))
	heads := prose.children(func(n *htmlNode) bool { return n.tag == "h2" || n.tag == "h3" })
	if len(heads) != len(post.TOC) {
		t.Fatalf("prose has %d h2/h3 headings, TOC has %d entries", len(heads), len(post.TOC))
	}
	for i, it := range post.TOC {
		if h := heads[i]; h.attr["id"] != it.ID || h.tag != "h"+strconv.Itoa(it.Level) || h.textContent() != collapse(it.Title) {
			t.Errorf("heading %d = <%s id=%q> %q, TOC entry = h%d #%s %q", i, h.tag, h.attr["id"], h.textContent(), it.Level, it.ID, it.Title)
		}
	}
	if len(post.TOC) == 0 {
		if n, m := len(doc.find(classIs("toc"))), len(doc.find(classIs("toc-list"))); n != 0 || m != 0 {
			t.Errorf("contents rendered for a post without headings: %d .toc, %d .toc-list", n, m)
		}
	} else {
		nav := doc.one(t, "nav.toc", all(tagIs("nav"), classIs("toc")))
		if nav.attr["aria-label"] != "Contents" {
			t.Errorf("nav.toc aria-label = %q, want “Contents”", nav.attr["aria-label"])
		}
		d := nav.one(t, "contents <details>", tagIs("details"))
		if _, open := d.attr["open"]; open {
			t.Error("the contents <details> is open; it starts closed at every width")
		}
		if s := d.one(t, "contents <summary>", tagIs("summary")); s.textContent() != "Contents" {
			t.Errorf("contents summary = %q, want “Contents”", s.textContent())
		}
		ol := nav.one(t, "ol.toc-list", all(tagIs("ol"), classIs("toc-list")))
		items := ol.children(tagIs("li"))
		if len(items) != len(post.TOC) {
			t.Fatalf("contents lists %d entries, want %d", len(items), len(post.TOC))
		}
		for i, it := range post.TOC {
			a := items[i].one(t, "toc link", tagIs("a"))
			if a.attr["href"] != "#"+it.ID || a.textContent() != it.Title || items[i].hasClass("sub") != (it.Level == 3) {
				t.Errorf("contents entry %d = %s %q sub=%v, want #%s %q sub=%v", i,
					a.attr["href"], a.textContent(), items[i].hasClass("sub"), it.ID, it.Title, it.Level == 3)
			}
		}
	}
	if n := len(doc.find(attrIs("aria-label", "Article tools"))); n != 0 {
		t.Errorf("%d “Article tools” asides; the rail was removed", n)
	}
	if n := len(doc.find(classIs("toc-m"))); n != 0 {
		t.Errorf("%d .toc-m elements; the sticky phone contents bar was removed", n)
	}
	if n := len(doc.find(hasAttr("data-toc-now"))); n != 0 {
		t.Errorf("%d [data-toc-now] elements; the scrollspy was removed", n)
	}

	// End matter: "Filed under a, b, c" as in-text links.
	tags := doc.find(classIs("article-tags"))
	if len(post.Tags) == 0 {
		if len(tags) != 0 {
			t.Error("“Filed under” on a post without tags")
		}
	} else {
		if len(tags) != 1 {
			t.Fatalf("“Filed under” lines: %d, want 1", len(tags))
		}
		if !tags[0].within(end) {
			t.Error("“Filed under” is outside footer.article-end")
		}
		links := tags[0].find(tagIs("a"))
		if len(links) != len(post.Tags) {
			t.Fatalf("filed under %d tags, want %d", len(links), len(post.Tags))
		}
		for i, tag := range post.Tags {
			if links[i].attr["href"] != "/tags/"+tag || links[i].textContent() != tag {
				t.Errorf("tag link %d = %s %q, want /tags/%s", i, links[i].attr["href"], links[i].textContent(), tag)
			}
		}
		if want := "Filed under " + strings.Join(post.Tags, ", "); tags[0].textContent() != collapse(want) {
			t.Errorf("tags line = %q, want %q", tags[0].textContent(), want)
		}
	}
	if n := len(doc.find(classIs("author"))); n != 0 {
		t.Errorf("%d author boxes; the author box was removed", n)
	}

	// Keep reading: entries with a title and a date, rel prev/next by direction.
	if len(keep) == 0 {
		if n := len(doc.find(classIs("keep"))); n != 0 {
			t.Errorf("%d .keep elements with nothing to suggest", n)
		}
	} else {
		keepNav := doc.one(t, "nav.keep", all(tagIs("nav"), classIs("keep"), attrIs("aria-labelledby", "keep-title")))
		if title := keepNav.one(t, "#keep-title", attrIs("id", "keep-title")); title.tag != "h2" || title.textContent() != "Keep reading" {
			t.Errorf("keep title = <%s> %q", title.tag, title.textContent())
		}
		items := keepNav.find(classIs("entry"))
		if len(items) != len(keep) {
			t.Fatalf("keep reading shows %d entries, want %d", len(items), len(keep))
		}
		for i, it := range keep {
			item := items[i]
			if !item.hasClass("row-link") {
				t.Errorf("keep entry %d class = %q, want a row-link", i, item.attr["class"])
			}
			a := item.one(t, "keep link", all(tagIs("a"), classIs("stretch")))
			wantRel := map[string]string{"prev": "prev", "next": "next"}[it.Dir]
			if a.attr["href"] != "/blog/"+it.Post.Slug || a.attr["rel"] != wantRel || a.textContent() != collapse(it.Post.Title) || a.parent.tag != "h3" {
				t.Errorf("keep entry %d link = %s rel=%q %q in <%s>", i, a.attr["href"], a.attr["rel"], a.textContent(), a.parent.tag)
			}
			if m, want := item.one(t, "keep meta", classIs("entry-meta")).textContent(), it.Post.Date.Format("2 Jan 2006"); m != want {
				t.Errorf("keep entry %d meta = %q, want %q", i, m, want)
			}
			if n := len(item.find(classIs("entry-desc"))); n != 0 {
				t.Errorf("keep entry %d has %d descriptions; keep reading shows title and date only", i, n)
			}
		}
	}
	if n := len(doc.find(classIs("keep-item--index"))); n != 0 {
		t.Errorf("%d index cards; the “All writing” card was removed", n)
	}

	if n := len(doc.find(tagIs("h1"))); n != 1 {
		t.Errorf("%d h1 elements", n)
	}
	checkNoSkippedLevels(t, doc)
}

func TestPostPages(t *testing.T) {
	s := newRealSite(t)
	app := newApp(s.h)
	blogs := s.engine.GetAllBlogs()
	if len(blogs) == 0 {
		t.Fatal("no published posts")
	}
	for _, post := range blogs {
		t.Run(post.Slug, func(t *testing.T) {
			resp := request(t, app, http.MethodGet, "/blog/"+post.Slug)
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d", resp.status)
			}
			newer, older := s.engine.GetAdjacentBlogs(post.Slug)
			keep := keepReading(post, older, newer, s.engine.GetSuggestedBlogs(post.Slug, keepReadingSlots+2))
			if len(blogs) > keepReadingSlots && len(keep) != keepReadingSlots {
				t.Fatalf("keep reading has %d items with %d posts", len(keep), len(blogs))
			}
			checkPost(t, resp.body, post, keep, s.h.config)
		})
	}
}

// A small site exercises what the real content does not: two keep-reading
// items, posts without tags, description or headings, an h3-only outline,
// and the 70-character title limit counted in characters, not bytes.
func TestPostPageEdgeCases(t *testing.T) {
	seventy := "Résumé parsing — "
	seventy += strings.Repeat("x", 70-utf8.RuneCountInString(seventy))
	posts := map[string]string{
		"bare.md": "---\ntitle: \"Bare\"\ndate: 2026-01-01\n---\n\nJust one paragraph.\n",
		"subs.md": "---\ntitle: \"" + seventy + "\"\ndate: 2026-01-02\ntags: [\"one\"]\ndescription: \"Only subsections.\"\n---\n\n### First\n\nText.\n\n### Second\n\nMore.\n",
		"long.md": "---\ntitle: \"" + seventy + "y\"\ndate: 2026-01-03\ntags: [\"a\", \"b\", \"c\", \"d\"]\ndescription: \"Four tags, one section.\"\n---\n\n## Alpha\n\nText.\n\n### Beta\n\nText.\n",
	}
	dir := t.TempDir()
	for name, src := range posts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	config := models.DefaultConfig()
	s := newSite(t, dir, realProjects, realTemplates, config)
	app := newApp(s.h)

	for _, slug := range []string{"bare", "subs", "long"} {
		t.Run(slug, func(t *testing.T) {
			post := s.engine.GetBlog(slug)
			resp := request(t, app, http.MethodGet, "/blog/"+slug)
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d", resp.status)
			}
			newer, older := s.engine.GetAdjacentBlogs(slug)
			keep := keepReading(post, older, newer, s.engine.GetSuggestedBlogs(slug, keepReadingSlots+2))
			if len(keep) != 2 {
				t.Fatalf("keep reading has %d items, want 2 on a three-post site", len(keep))
			}
			checkPost(t, resp.body, post, keep, config)

			// Spot checks, so the data-driven ones above cannot pass vacuously.
			doc := parseHTML(t, resp.body)
			h1 := doc.one(t, "h1", tagIs("h1"))
			switch slug {
			case "bare":
				if len(doc.find(classIs("toc-list"))) != 0 || len(doc.find(classIs("article-tags"))) != 0 || len(doc.find(classIs("article-dek"))) != 0 {
					t.Error("contents, “Filed under” or a dek rendered for a bare post")
				}
			case "subs":
				if utf8.RuneCountInString(post.Title) != 70 || len(post.Title) <= 70 || h1.hasClass("article-title--long") {
					t.Errorf("a 70-character, %d-byte title got article-title--long", len(post.Title))
				}
				if subs := doc.find(all(tagIs("li"), classIs("sub"))); len(subs) != 2 {
					t.Errorf("%d sub entries, want 2 in the one contents list", len(subs))
				}
			case "long":
				if !h1.hasClass("article-title--long") {
					t.Error("a 71-character title did not get article-title--long")
				}
				if got := doc.one(t, ".article-tags", classIs("article-tags")).textContent(); got != "Filed under a, b, c, d" {
					t.Errorf("tags line = %q, want all four tags", got)
				}
			}
			if n := len(doc.one(t, "nav.keep", all(tagIs("nav"), classIs("keep"))).find(classIs("entry"))); n != 2 {
				t.Errorf("keep reading shows %d entries, want 2", n)
			}
			if card := doc.find(classIs("keep-item--index")); len(card) != 0 {
				t.Errorf("%d index cards, want none", len(card))
			}
		})
	}
}

// checkErrorPage asserts the frame every status shares and returns the
// parsed page.
func checkErrorPage(t *testing.T, resp response, code int, heading, message string) *htmlNode {
	t.Helper()
	if resp.status != code {
		t.Fatalf("status = %d, want %d", resp.status, code)
	}
	doc := parseHTML(t, resp.body)
	main := doc.one(t, "main#main", attrIs("id", "main"))
	if main.attr["class"] != "error" {
		t.Errorf(`main class = %q, want "error"`, main.attr["class"])
	}
	// <title> is the handler's heading for every status, also on a 404,
	// whose visible h1 is the design's sentence instead.
	if docTitle, _ := errorText(code); pageTitle(t, doc) != docTitle+" · "+models.DefaultConfig().Title {
		t.Errorf("<title> = %q, want %q", pageTitle(t, doc), docTitle+" · "+models.DefaultConfig().Title)
	}
	if len(doc.find(all(tagIs("meta"), attrIs("name", "robots"), attrIs("content", "noindex")))) != 1 {
		t.Error("error page is not noindex")
	}
	if len(doc.find(classIs("progress"))) != 0 {
		t.Error("error page has the reading-progress bar")
	}

	if n := len(main.find(classIs("label"))); n != 0 {
		t.Errorf("%d .label elements in main; the rail label was removed", n)
	}
	numeral := main.one(t, ".error-code", classIs("error-code"))
	if numeral.attr["aria-hidden"] != "true" || numeral.textContent() != strconv.Itoa(code) {
		t.Errorf("numeral = %q aria-hidden=%q", numeral.textContent(), numeral.attr["aria-hidden"])
	}
	h1 := doc.one(t, "h1", tagIs("h1"))
	if !h1.hasClass("error-title") || h1.textContent() != heading {
		t.Errorf("h1 = %q, want %q", h1.textContent(), heading)
	}
	if n := len(main.find(classIs("request-line"))); n != 0 {
		t.Errorf("%d request lines; the address bar shows the path", n)
	}
	msg := main.one(t, ".error-msg", classIs("error-msg"))
	if msg.textContent() != message {
		t.Errorf("message = %q, want %q", msg.textContent(), message)
	}

	// The ways out: the 404's message links them; other statuses go home.
	if code == http.StatusNotFound {
		var ways []string
		for _, a := range msg.find(tagIs("a")) {
			ways = append(ways, a.attr["href"]+" "+a.textContent())
		}
		if want := []string{"/blog writing index", "/search search"}; !slices.Equal(ways, want) {
			t.Errorf("ways out = %q, want %q", ways, want)
		}
	} else {
		home := main.one(t, "p.more", all(tagIs("p"), classIs("more"))).one(t, "p.more a", tagIs("a"))
		if home.attr["href"] != "/" || home.textContent() != "Go home" {
			t.Errorf("way out = %s %q, want / “Go home”", home.attr["href"], home.textContent())
		}
	}
	if n := len(main.find(hasAttr("data-open-palette"))); n != 0 {
		t.Errorf("%d palette buttons in main; search is in the header", n)
	}
	if n := len(main.find(classIs("hero-cta"))); n != 0 {
		t.Errorf("%d button rows in main; the buttons were removed", n)
	}
	checkNoSkippedLevels(t, doc)
	return doc
}

const notFoundMessage = "The page may have moved, or the link that brought you here has a typo. The writing index and search cover everything that is here."

func TestErrorPageNotFound(t *testing.T) {
	app := newApp(newRealSite(t).h)
	tests := []struct{ method, path string }{
		{http.MethodGet, "/nope"},
		{http.MethodGet, "/deeply/nested/unknown/path"},
		{http.MethodGet, "/blog/no-such-post"},
		{http.MethodGet, "/tags/no-such-tag"},
		{http.MethodGet, "/blog/no-such-post/download/epub"},
		{http.MethodPost, "/blog"},
		{http.MethodDelete, "/about"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp := request(t, app, tt.method, tt.path)
			doc := checkErrorPage(t, resp, http.StatusNotFound, "Nothing is listening on this route.", notFoundMessage)
			// The redline is home's alone: the 404's em is plain italic.
			if n := len(doc.find(classIs("redline"))); n != 0 {
				t.Errorf("%d redlines on the 404; only home has one", n)
			}
			if ems := doc.one(t, "h1", tagIs("h1")).find(tagIs("em")); len(ems) != 1 || ems[0].textContent() != "this route." {
				t.Errorf("h1 em = %q, want one em “this route.”", texts(ems))
			}
		})
	}
}

func TestErrorPageOtherStatuses(t *testing.T) {
	h := newRealSite(t).h
	app := newApp(h, func(app *fiber.App) {
		app.Use(recover.New())
		app.Get("/boom", func(c *fiber.Ctx) error { return errors.New("secret database detail") })
		app.Get("/panic", func(c *fiber.Ctx) error { panic("secret panic detail") })
		app.Get("/unavailable", func(c *fiber.Ctx) error {
			return fiber.NewError(http.StatusServiceUnavailable, "secret upstream detail")
		})
		app.Get("/teapot", func(c *fiber.Ctx) error { return fiber.NewError(http.StatusTeapot, "secret teapot detail") })
	})
	tests := []struct {
		path string
		code int
	}{
		{"/boom", http.StatusInternalServerError},
		{"/panic", http.StatusInternalServerError},
		{"/unavailable", http.StatusServiceUnavailable},
		{"/teapot", http.StatusTeapot},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			heading, message := errorText(tt.code)
			resp := request(t, app, http.MethodGet, tt.path)
			doc := checkErrorPage(t, resp, tt.code, heading, message)
			if strings.Contains(resp.body, "secret") {
				t.Error("error detail leaked to the client")
			}
			if n := len(doc.find(classIs("redline"))); n != 0 {
				t.Errorf("%d redlines on a %d page; only home has one", n, tt.code)
			}
			if strings.Contains(resp.body, "this route") || strings.Contains(resp.body, "Not Found") {
				t.Error("404 copy on a non-404 page")
			}
		})
	}
}

// The requested path is reflected in og:url, so it must come out escaped.
func TestErrorPageEscapesThePath(t *testing.T) {
	app := newApp(newRealSite(t).h)
	resp := request(t, app, http.MethodGet, `/x"><script>alert(1)</script>`)
	if resp.status != http.StatusNotFound {
		t.Fatalf("status = %d", resp.status)
	}
	if strings.Contains(resp.body, "<script>alert(1)") {
		t.Fatal("the path was written into the page unescaped")
	}
	// the path adds no script element: as many as on any other 404
	if got, want := strings.Count(resp.body, "<script"), strings.Count(request(t, app, http.MethodGet, "/nope").body, "<script"); got != want {
		t.Errorf("%d <script elements, want %d as on any 404", got, want)
	}
}

// The home page lists at most three projects, featured first, each with its
// short summary as a sentence: ShortSummary drops a first sentence's full
// stop and the row sets one back, unless the summary already ends with ".",
// "!" or "?". "All projects" appears only when /projects has more, and the
// section is left out when there are none.
func TestHomeProjects(t *testing.T) {
	type row struct{ title, href, desc string }
	tests := []struct {
		name, projects string
		rows           []row
		more           bool
		absent         []string
	}{
		{"many", `[
	{"id": "a", "title": "Alpha", "description": "First project.", "featured": true},
	{"id": "b", "title": "Beta", "description": "Second project.", "featured": true},
	{"id": "c", "title": "Gamma", "description": "Third project.", "featured": true},
	{"id": "qr", "title": "pixelQR", "description": "pixelQR: QR code generation and management.", "featured": true},
	{"id": "ride", "title": "rideshare", "description": "An in-memory API. More.", "summary": "A carpooling API"},
	{"id": "solo", "title": "Solo"}
]`, []row{{"Alpha", "/projects#a", "First project."}, {"Beta", "/projects#b", "Second project."}, {"Gamma", "/projects#c", "Third project."}},
			true, []string{"pixelQR", "rideshare", "Solo"}},
		{"edge", `[{"id":"qr","title":"pixelQR","description":"pixelQR: QR code generation and management.","featured":true},{"id":"solo","title":"Solo","featured":true}]`,
			[]row{{"pixelQR", "/projects#qr", "QR code generation and management."}, {"Solo", "/projects#solo", ""}}, false, nil},
		{"others", `[{"id":"ride","title":"rideshare","description":"An in-memory API. More.","summary":"A carpooling API"}]`,
			[]row{{"rideshare", "/projects#ride", "A carpooling API."}}, false, nil},
		{"punctuated", `[
	{"id": "q", "title": "Q", "description": "Why not? Because.", "featured": true},
	{"id": "s", "title": "S", "summary": "Ends with a stop.", "featured": true},
	{"id": "x", "title": "X", "summary": "Loud!"}
]`, []row{{"Q", "/projects#q", "Why not?"}, {"S", "/projects#s", "Ends with a stop."}, {"X", "/projects#x", "Loud!"}}, false, nil},
		{"none", `[]`, nil, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "projects.json")
			if err := os.WriteFile(file, []byte(tt.projects), 0o644); err != nil {
				t.Fatal(err)
			}
			s := newSite(t, realContent, file, realTemplates, models.DefaultConfig())
			resp := request(t, newApp(s.h), http.MethodGet, "/")
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d", resp.status)
			}
			doc := parseHTML(t, resp.body)
			work := doc.find(all(tagIs("section"), attrIs("id", "work")))
			if len(tt.rows) == 0 {
				if len(work) != 0 {
					t.Error("a Projects section on a site without projects")
				}
			} else {
				if len(work) != 1 {
					t.Fatalf("%d section#work elements, want 1", len(work))
				}
				if h2 := work[0].one(t, "#work h2", tagIs("h2")); h2.textContent() != "Projects" {
					t.Errorf("#work h2 = %q, want “Projects”", h2.textContent())
				}
				var got []row
				for i, e := range work[0].find(classIs("entry")) {
					a := e.one(t, "project link", all(tagIs("a"), classIs("stretch")))
					r := row{a.textContent(), a.attr["href"], ""}
					switch d := e.find(classIs("entry-desc")); len(d) {
					case 0:
					case 1:
						r.desc = d[0].textContent()
					default:
						t.Errorf("project row %d has %d descriptions", i, len(d))
					}
					got = append(got, r)
				}
				if !slices.Equal(got, tt.rows) {
					t.Errorf("project rows = %q, want %q", got, tt.rows)
				}
				more := work[0].find(all(tagIs("p"), classIs("more")))
				if tt.more {
					if len(more) != 1 {
						t.Fatalf("%d p.more in #work, want 1", len(more))
					}
					if a := more[0].one(t, "p.more a", tagIs("a")); a.attr["href"] != "/projects" || a.textContent() != "All projects" {
						t.Errorf("more link = %s %q, want /projects “All projects”", a.attr["href"], a.textContent())
					}
				} else if len(more) != 0 {
					t.Errorf("%d p.more in #work with every project shown", len(more))
				}
			}
			if !tt.more && strings.Contains(resp.body, "All projects") {
				t.Error("“All projects” with every project shown")
			}
			for _, a := range doc.find(all(tagIs("a"), classIs("stretch"))) {
				if slices.Contains(tt.absent, a.textContent()) {
					t.Errorf("%s has a row past the first three", a.textContent())
				}
			}
			if n := len(doc.find(tagIs("h1"))); n != 1 {
				t.Errorf("%d h1 elements", n)
			}
			checkNoSkippedLevels(t, doc)
		})
	}
}

// The home page's Writing section lists the newest three posts (title and
// date), adds "All writing" only when there are more, and is left out on a
// site without posts.
func TestHomeWriting(t *testing.T) {
	for _, n := range []int{0, 1, 3, 4} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			dir := t.TempDir()
			for i := 1; i <= n; i++ {
				src := fmt.Sprintf("---\ntitle: \"Post %d\"\ndate: 2026-01-0%d\ndescription: \"About %d.\"\n---\n\nText.\n", i, i, i)
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("p%d.md", i)), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s := newSite(t, dir, realProjects, realTemplates, models.DefaultConfig())
			resp := request(t, newApp(s.h), http.MethodGet, "/")
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d", resp.status)
			}
			doc := parseHTML(t, resp.body)
			writing := doc.find(all(tagIs("section"), attrIs("id", "writing")))
			if n == 0 {
				if len(writing) != 0 {
					t.Error("a Writing section on a site without posts")
				}
				return
			}
			if len(writing) != 1 {
				t.Fatalf("%d section#writing elements, want 1", len(writing))
			}
			var got, want []string
			for i := n; i > n-min(n, 3); i-- { // newest first
				want = append(want, fmt.Sprintf("/blog/p%d Post %d · %d Jan 2026", i, i, i))
			}
			for _, e := range writing[0].find(classIs("entry")) {
				a := e.one(t, "post link", all(tagIs("a"), classIs("stretch")))
				got = append(got, a.attr["href"]+" "+a.textContent()+" · "+e.one(t, "post date", classIs("entry-meta")).textContent())
				if d := e.find(classIs("entry-desc")); len(d) != 0 {
					t.Errorf("%s shows its description; home rows are title and date", a.attr["href"])
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("writing rows = %q, want %q", got, want)
			}
			more := writing[0].find(all(tagIs("p"), classIs("more")))
			switch {
			case n > 3 && len(more) != 1:
				t.Fatalf("%d p.more with %d posts, want 1", len(more), n)
			case n > 3:
				if a := more[0].one(t, "p.more a", tagIs("a")); a.attr["href"] != "/blog" || a.textContent() != "All writing" {
					t.Errorf("more link = %s %q, want /blog “All writing”", a.attr["href"], a.textContent())
				}
			case len(more) != 0:
				t.Errorf("“All writing” with every one of %d posts shown", n)
			}
			checkNoSkippedLevels(t, doc)
		})
	}
}

// The projects page lists every project, featured first, then closes with
// the GitHub line. A project's install command is shown in full with a Copy
// button, and replaces its "Live demo" link.
func TestProjectsList(t *testing.T) {
	config := models.DefaultConfig()
	get := func(t *testing.T, projects string) *htmlNode {
		t.Helper()
		file := filepath.Join(t.TempDir(), "projects.json")
		if err := os.WriteFile(file, []byte(projects), 0o644); err != nil {
			t.Fatal(err)
		}
		s := newSite(t, realContent, file, realTemplates, config)
		resp := request(t, newApp(s.h), http.MethodGet, "/projects")
		if resp.status != http.StatusOK {
			t.Fatalf("status = %d", resp.status)
		}
		doc := parseHTML(t, resp.body)
		if n := len(doc.find(tagIs("h1"))); n != 1 {
			t.Errorf("%d h1 elements", n)
		}
		checkNoSkippedLevels(t, doc)
		return doc
	}

	t.Run("mixed", func(t *testing.T) {
		doc := get(t, `[{"id": "a", "title": "A"}, {"id": "b", "title": "B", "featured": true}]`)
		if n := len(doc.find(classIs("page-facts"))); n != 0 {
			t.Errorf("%d .page-facts; the facts were removed", n)
		}
		var ids []string
		for _, p := range doc.find(all(tagIs("article"), classIs("project"))) {
			id := p.attr["id"]
			ids = append(ids, id)
			p.one(t, "h2#p-"+id, all(tagIs("h2"), attrIs("id", "p-"+id)))
		}
		if want := []string{"b", "a"}; !slices.Equal(ids, want) {
			t.Errorf("projects = %q, want %q (featured first)", ids, want)
		}
		main := doc.one(t, "main#main", attrIs("id", "main"))
		gh := main.find(all(tagIs("a"), attrIs("href", config.GitHubURL)))
		if len(gh) != 1 {
			t.Fatalf("%d links to %s in main, want 1", len(gh), config.GitHubURL)
		}
		if p := gh[0].parent; p.tag != "p" || !p.hasClass("more") || p.textContent() != "Everything else lives on GitHub." {
			t.Errorf("GitHub line = <%s class=%q> %q", p.tag, p.attr["class"], p.textContent())
		}
	})

	t.Run("install", func(t *testing.T) {
		cmd := "curl -fsSL https://example.com/i.sh | bash"
		doc := get(t, `[{"id": "w", "title": "W", "description": "Does a thing.", "install": "`+cmd+`", "github_link": "https://github.com/x/w", "demo_link": "https://example.com/i.sh", "featured": true}]`)
		box := doc.one(t, ".install", classIs("install"))
		if box.attr["role"] != "group" || box.attr["aria-labelledby"] != "install-w" {
			t.Errorf("install panel role=%q aria-labelledby=%q, want group, install-w", box.attr["role"], box.attr["aria-labelledby"])
		}
		if l := doc.one(t, "#install-w", attrIs("id", "install-w")); l.textContent() != "Install W" {
			t.Errorf("install label = %q, want “Install W”", l.textContent())
		}
		btn := box.one(t, "button.install__copy", all(tagIs("button"), classIs("install__copy")))
		if btn.attr["type"] != "button" || btn.attr["data-copy"] != cmd || btn.attr["aria-label"] != "Copy the W install command" || btn.textContent() != "Copy" {
			t.Errorf("install copy button = %v %q", btn.attr, btn.textContent())
		}
		code := box.one(t, "pre.install__cmd", all(tagIs("pre"), classIs("install__cmd"))).one(t, "install code", tagIs("code"))
		if code.textContent() != cmd {
			t.Errorf("install command = %q, want %q", code.textContent(), cmd)
		}
		// The URL may wrap after its path slash (example.com/<wbr>i.sh), never
		// inside "https://" and never mid-word.
		if n := len(code.find(tagIs("wbr"))); n != 1 {
			t.Errorf("%d <wbr> in the install command, want 1 (after the path slash)", n)
		}
		// One project with its source linked: no plural "each one" copy.
		if d := doc.one(t, ".page-dek", classIs("page-dek")).textContent(); d != "Things I’ve built." {
			t.Errorf("dek = %q, want “Things I’ve built.”", d)
		}
		links := doc.one(t, ".links", classIs("links")).find(tagIs("a"))
		if len(links) != 1 || links[0].textContent() != "Source: W" || links[0].attr["href"] != "https://github.com/x/w" {
			t.Errorf("project links = %q, want one “Source: W”", texts(links))
		}
		for _, a := range doc.find(tagIs("a")) {
			if strings.HasPrefix(a.textContent(), "Live demo") {
				t.Errorf("a “Live demo” link (%s) next to the install command", a.attr["href"])
			}
		}
	})

	t.Run("demo", func(t *testing.T) {
		doc := get(t, `[{"id": "d", "title": "D", "github_link": "https://github.com/x/d", "demo_link": "https://d.example"}]`)
		links := doc.one(t, ".links", classIs("links"))
		var hrefs []string
		for _, a := range links.find(tagIs("a")) {
			hrefs = append(hrefs, a.attr["href"])
		}
		if want := []string{"https://github.com/x/d", "https://d.example"}; !slices.Equal(hrefs, want) {
			t.Errorf("project links = %q, want %q", hrefs, want)
		}
		// A space between the links: reader mode and copied text must not
		// run them together as "Source: DLive demo: D".
		if got := links.textContent(); got != "Source: D Live demo: D" {
			t.Errorf("links text = %q, want “Source: D Live demo: D”", got)
		}
	})

	t.Run("none", func(t *testing.T) {
		doc := get(t, `[]`)
		if n := len(doc.find(all(tagIs("article"), classIs("project")))); n != 0 {
			t.Errorf("%d projects on a site without any", n)
		}
		if m := doc.one(t, "p.more", all(tagIs("p"), classIs("more"))).textContent(); m != "Everything lives on GitHub." {
			t.Errorf("GitHub line = %q, want “Everything lives on GitHub.”", m)
		}
		if d := doc.one(t, ".page-dek", classIs("page-dek")).textContent(); d != "Things I’ve built." {
			t.Errorf("dek = %q, want “Things I’ve built.”", d)
		}
	})
}

// The frame every page shares: the name, the four section links and the
// footer links. The revision is a <meta> in the head now, not footer text.
func TestSiteChrome(t *testing.T) {
	config := models.DefaultConfig()
	config.Revision = "abc1234"
	s := newSite(t, realContent, realProjects, realTemplates, config)
	app := newApp(s.h)

	resp := request(t, app, http.MethodGet, "/")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d", resp.status)
	}
	doc := parseHTML(t, resp.body)
	var nav []string
	for _, a := range doc.one(t, "nav.nav", all(tagIs("nav"), classIs("nav"))).find(tagIs("a")) {
		nav = append(nav, a.attr["href"]+" "+a.textContent())
	}
	if want := []string{"/blog Writing", "/projects Projects", "/about About", "/contact Contact"}; !slices.Equal(nav, want) {
		t.Errorf("nav = %q, want %q", nav, want)
	}
	if brand := doc.one(t, "a.brand", all(tagIs("a"), classIs("brand"))); brand.attr["aria-current"] != "page" {
		t.Errorf("brand aria-current = %q on home, want page", brand.attr["aria-current"])
	}

	// The header's two icon buttons: search opens the palette (and is
	// replaced by a link to /search without JS), and the theme toggle.
	hdr := doc.one(t, "site header", all(tagIs("header"), classIs("site-header")))
	if b := hdr.one(t, "search button", hasAttr("data-open-palette")); b.tag != "button" || b.attr["type"] != "button" || b.attr["aria-haspopup"] != "dialog" || b.attr["aria-label"] == "" {
		t.Errorf("search button = <%s> %v, want a type=button with aria-haspopup=dialog and a name", b.tag, b.attr)
	}
	ns := hdr.one(t, "header <noscript>", tagIs("noscript")).one(t, "no-JS search link", tagIs("a"))
	if ns.attr["href"] != "/search" || !ns.hasClass("icon-btn") || ns.attr["aria-label"] == "" {
		t.Errorf("no-JS search link = %v, want an a.icon-btn to /search with a name", ns.attr)
	}
	if b := hdr.one(t, "theme toggle", all(tagIs("button"), classIs("theme-toggle"))); b.attr["type"] != "button" || b.attr["aria-label"] == "" {
		t.Errorf("theme toggle = %v, want a type=button with a name", b.attr)
	}

	footer := doc.one(t, "site footer", all(tagIs("footer"), classIs("site-footer")))
	var links, rels []string
	for _, a := range footer.find(tagIs("a")) {
		links = append(links, a.attr["href"]+" "+a.textContent())
		rels = append(rels, a.attr["rel"])
	}
	wantLinks := []string{config.GitHubURL + " GitHub", config.LinkedInURL + " LinkedIn", config.XURL + " X", "/rss.xml RSS", "mailto:" + config.Email + " Email"}
	if !slices.Equal(links, wantLinks) {
		t.Errorf("footer links = %q, want %q", links, wantLinks)
	}
	if want := []string{"me", "me", "me", "", ""}; !slices.Equal(rels, want) {
		t.Errorf("footer rels = %q, want %q", rels, want)
	}
	if !strings.Contains(resp.body, `<meta name="revision" content="abc1234">`) {
		t.Error(`no <meta name="revision" content="abc1234"> in the page`)
	}
	if strings.Contains(footer.textContent(), "abc1234") {
		t.Error("the revision is still footer text")
	}

	if list := doc.one(t, "#palette-list", attrIs("id", "palette-list")); list.attr["data-index"] != "/search-index.json" {
		t.Errorf("palette data-index = %q", list.attr["data-index"])
	}
	if input := doc.one(t, "#palette-input", attrIs("id", "palette-input")); input.attr["role"] != "combobox" {
		t.Errorf("palette input role = %q, want combobox", input.attr["role"])
	}
	if n := len(doc.find(classIs("palette-foot"))); n != 0 {
		t.Errorf("%d palette footers; the hint row was removed", n)
	}

	blog := parseHTML(t, request(t, app, http.MethodGet, "/blog").body)
	if c, ok := blog.one(t, "a.brand", all(tagIs("a"), classIs("brand"))).attr["aria-current"]; ok {
		t.Errorf("brand aria-current = %q on /blog, want none", c)
	}
}
