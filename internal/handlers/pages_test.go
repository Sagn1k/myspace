package handlers

// Rendering checks for the article page (blog_post.html) and the error page
// (error.html) with the real templates. They cover what a browser check of
// the live site cannot reach: 5xx error pages, a site with fewer than four
// posts, and posts without tags, a description or headings.

import (
	"encoding/xml"
	"errors"
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

	// The reading-progress bar fills base.html's header-extra hook.
	header := doc.one(t, "site header", all(tagIs("header"), classIs("site-header")))
	bar := doc.one(t, ".progress", classIs("progress"))
	if !bar.within(header) || bar.attr["aria-hidden"] != "true" || len(bar.children(tagIs("span"))) != 1 {
		t.Error(`want <div class="progress" aria-hidden="true"><span></span></div> inside the site header`)
	}

	// Opener
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
	// "Writing / 2026": the link, an aria-hidden slash, the post's year.
	crumbs := head.one(t, "breadcrumb", all(tagIs("nav"), classIs("crumbs"), attrIs("aria-label", "Breadcrumb")))
	parts := crumbs.children(func(*htmlNode) bool { return true })
	if len(parts) != 3 || parts[0].tag != "a" || parts[0].attr["href"] != "/blog" || parts[0].textContent() != "Writing" ||
		parts[1].attr["aria-hidden"] != "true" || parts[2].textContent() != post.Date.Format("2006") {
		t.Errorf("breadcrumb = %q", texts(parts))
	}
	kicker := head.find(classIs("label--accent"))
	if len(post.Tags) == 0 {
		if len(kicker) != 0 {
			t.Errorf("tag kicker %q on a post without tags", texts(kicker))
		}
	} else if want := strings.Join(post.Tags[:min(3, len(post.Tags))], " · "); len(kicker) != 1 || kicker[0].textContent() != want {
		t.Errorf("tag kicker = %q, want %q", texts(kicker), want)
	}
	dek := head.find(classIs("article-dek"))
	if post.Description == "" {
		if len(dek) != 0 {
			t.Error("dek on a post without a description")
		}
	} else if len(dek) != 1 || dek[0].textContent() != collapse(post.Description) {
		t.Errorf("dek = %q, want the description", texts(dek))
	}

	sections := 0
	for _, it := range post.TOC {
		if it.Level == 2 {
			sections++
		}
	}
	sectionsText := strconv.Itoa(sections) + " sections"
	if sections == 1 {
		sectionsText = "1 section"
	}
	facts := head.one(t, ".post-facts", classIs("post-facts"))
	tm := facts.one(t, "post date", tagIs("time"))
	if tm.attr["datetime"] != post.Date.Format("2006-01-02") || tm.textContent() != post.Date.Format("2 January 2006") {
		t.Errorf("post date = %s %q", tm.attr["datetime"], tm.textContent())
	}
	wantFacts := []string{strconv.Itoa(post.ReadingTime) + " min read"}
	if sections > 0 {
		wantFacts = append(wantFacts, sectionsText)
	}
	if got := texts(facts.find(classIs("meta"))); !slices.Equal(got, wantFacts) {
		t.Errorf("post facts = %q, want %q", got, wantFacts)
	}

	// PDF, EPUB and Copy link, once in the opener (phones) and once in the rail.
	actionRows := []*htmlNode{
		head.one(t, ".post-actions", classIs("post-actions")),
		doc.one(t, ".toc-foot", classIs("toc-foot")),
	}
	for _, row := range actionRows {
		var hrefs []string
		for _, a := range row.find(tagIs("a")) {
			if a.attr["href"] != "#top" { // the rail's "Back to top"
				hrefs = append(hrefs, a.attr["href"])
			}
		}
		if want := []string{"/blog/" + post.Slug + "/download/pdf", "/blog/" + post.Slug + "/download/epub"}; !slices.Equal(hrefs, want) {
			t.Errorf("%s links = %q, want %q", row.attr["class"], hrefs, want)
		}
		copyBtn := row.one(t, "copy link button", hasAttr("data-copy-url"))
		if copyBtn.tag != "button" || copyBtn.attr["type"] != "button" || copyBtn.attr["aria-label"] == "" || copyBtn.textContent() != "Copy link" {
			t.Errorf("copy link control = <%s %v> %q", copyBtn.tag, copyBtn.attr, copyBtn.textContent())
		}
	}

	// Contents: the rail and the phone <details> both list exactly the prose's
	// top-level h2/h3 headings, in order, so the CSS counters agree.
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
	aside := doc.one(t, "article tools", all(tagIs("aside"), attrIs("aria-label", "Article tools")))
	railNav := aside.find(all(tagIs("nav"), classIs("toc")))
	mobile := doc.find(all(tagIs("details"), classIs("toc-m")))
	if len(post.TOC) == 0 {
		if len(railNav) != 0 || len(mobile) != 0 {
			t.Error("contents lists rendered for a post without headings")
		}
		box := aside.one(t, "div.toc", all(tagIs("div"), classIs("toc")))
		if !actionRows[1].within(box) {
			t.Error("the rail's actions are not in the .toc box")
		}
	} else {
		if len(railNav) != 1 || len(mobile) != 1 {
			t.Fatalf("contents lists: %d rail, %d mobile; want 1 each", len(railNav), len(mobile))
		}
		if railNav[0].attr["aria-labelledby"] != "toc-title" || doc.one(t, "#toc-title", attrIs("id", "toc-title")).textContent() != "Contents" {
			t.Error("the rail contents is not labelled “Contents”")
		}
		if !actionRows[1].within(railNav[0]) {
			t.Error("the rail's actions are not in nav.toc")
		}
		for _, list := range []*htmlNode{railNav[0], mobile[0]} {
			ol := list.one(t, "ol.toc-list", all(tagIs("ol"), classIs("toc-list")))
			items := ol.children(tagIs("li"))
			if len(items) != len(post.TOC) {
				t.Fatalf("%s lists %d entries, want %d", list.attr["class"], len(items), len(post.TOC))
			}
			for i, it := range post.TOC {
				a := items[i].one(t, "toc link", tagIs("a"))
				if a.attr["href"] != "#"+it.ID || a.textContent() != it.Title || items[i].hasClass("sub") != (it.Level == 3) {
					t.Errorf("%s entry %d = %s %q sub=%v, want #%s %q sub=%v", list.attr["class"], i,
						a.attr["href"], a.textContent(), items[i].hasClass("sub"), it.ID, it.Title, it.Level == 3)
				}
			}
		}
		headMeta := railNav[0].one(t, ".toc-head", classIs("toc-head")).find(classIs("meta"))
		now := mobile[0].one(t, "[data-toc-now]", hasAttr("data-toc-now"))
		if sections > 0 {
			if len(headMeta) != 1 || headMeta[0].textContent() != sectionsText || now.textContent() != sectionsText {
				t.Errorf("sections count = %q / %q, want %q", texts(headMeta), now.textContent(), sectionsText)
			}
		} else if len(headMeta) != 0 || now.textContent() != "" {
			t.Errorf("sections count shown without h2 sections: %q / %q", texts(headMeta), now.textContent())
		}
	}

	// End matter
	filed := doc.find(classIs("filed"))
	if len(post.Tags) == 0 {
		if len(filed) != 0 {
			t.Error("“Filed under” on a post without tags")
		}
	} else {
		if len(filed) != 1 {
			t.Fatalf("“Filed under” blocks: %d", len(filed))
		}
		chips := filed[0].find(all(tagIs("a"), classIs("chip")))
		if len(chips) != len(post.Tags) {
			t.Fatalf("filed under %d tags, want %d", len(chips), len(post.Tags))
		}
		for i, tag := range post.Tags {
			if chips[i].attr["href"] != "/tags/"+tag || chips[i].textContent() != tag {
				t.Errorf("tag chip %d = %s %q, want /tags/%s", i, chips[i].attr["href"], chips[i].textContent(), tag)
			}
		}
	}
	author := doc.one(t, "author note", all(tagIs("section"), classIs("author")))
	if note := author.textContent(); !strings.Contains(note, config.Author+" is a software engineer with "+config.YearsExperience+" years") {
		t.Errorf("author note = %q", note)
	}
	var authorLinks []string
	for _, a := range author.find(tagIs("a")) {
		authorLinks = append(authorLinks, a.attr["href"]+" "+a.textContent())
	}
	if want := []string{"/about About " + strings.Fields(config.Author)[0] + " →", config.GitHubURL + " GitHub ↗", "/rss.xml RSS feed →"}; !slices.Equal(authorLinks, want) {
		t.Errorf("author links = %q, want %q", authorLinks, want)
	}

	keepNav := doc.one(t, "nav.keep", all(tagIs("nav"), classIs("keep"), attrIs("aria-labelledby", "keep-title")))
	if title := keepNav.one(t, "#keep-title", attrIs("id", "keep-title")); title.tag != "h2" || title.textContent() != "Keep reading" {
		t.Errorf("keep title = <%s> %q", title.tag, title.textContent())
	}
	items := keepNav.find(classIs("keep-item"))
	wantItems := len(keep)
	if len(keep) < 3 {
		wantItems++
	}
	if len(items) != wantItems {
		t.Fatalf("keep reading shows %d items, want %d (%d posts + index card)", len(items), wantItems, len(keep))
	}
	labels := map[string]string{"prev": "← Previous", "next": "Next →", "related": "Related", "more": "More writing"}
	for i, it := range keep {
		item := items[i]
		if item.hasClass("keep-item--index") || !item.hasClass("row-link") {
			t.Errorf("keep item %d class = %q", i, item.attr["class"])
		}
		dir := item.one(t, ".dir", classIs("dir"))
		if l := dir.find(classIs("label")); len(l) != 1 || l[0].textContent() != labels[it.Dir] {
			t.Errorf("keep item %d label = %q, want %q", i, texts(l), labels[it.Dir])
		}
		meta := dir.one(t, "keep meta", classIs("meta"))
		if want := it.Post.Date.Format("2 Jan 2006") + " · " + strconv.Itoa(it.Post.ReadingTime) + " min"; meta.textContent() != want {
			t.Errorf("keep item %d meta = %q, want %q", i, meta.textContent(), want)
		}
		a := item.one(t, "keep link", all(tagIs("a"), classIs("stretch")))
		wantRel := map[string]string{"prev": "prev", "next": "next"}[it.Dir]
		if a.attr["href"] != "/blog/"+it.Post.Slug || a.attr["rel"] != wantRel || a.textContent() != collapse(it.Post.Title) || a.parent.tag != "h3" {
			t.Errorf("keep item %d link = %s rel=%q %q in <%s>", i, a.attr["href"], a.attr["rel"], a.textContent(), a.parent.tag)
		}
		if desc := item.children(tagIs("p")); it.Post.Description != "" && (len(desc) != 2 || desc[1].textContent() != collapse(it.Post.Description)) {
			t.Errorf("keep item %d description = %q", i, texts(desc))
		}
	}
	if len(keep) < 3 {
		card := items[len(items)-1]
		a := card.one(t, "index card link", tagIs("a"))
		if !card.hasClass("keep-item--index") || a.attr["href"] != "/blog" || a.textContent() != "All writing" {
			t.Errorf("index card = %q %s %q", card.attr["class"], a.attr["href"], a.textContent())
		}
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

// A small site exercises what the real content does not: the index card
// (fewer than three posts to suggest), posts without tags, description or
// headings, an h3-only outline, and the 70-character title limit counted in
// characters, not bytes.
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
				if len(doc.find(classIs("toc-list"))) != 0 || len(doc.find(classIs("filed"))) != 0 || len(doc.find(classIs("article-dek"))) != 0 {
					t.Error("contents, “Filed under” or a dek rendered for a bare post")
				}
			case "subs":
				if utf8.RuneCountInString(post.Title) != 70 || len(post.Title) <= 70 || h1.hasClass("article-title--long") {
					t.Errorf("a 70-character, %d-byte title got article-title--long", len(post.Title))
				}
				if subs := doc.find(all(tagIs("li"), classIs("sub"))); len(subs) != 4 {
					t.Errorf("%d sub entries, want 2 in each contents list", len(subs))
				}
			case "long":
				if !h1.hasClass("article-title--long") {
					t.Error("a 71-character title did not get article-title--long")
				}
				if got := doc.one(t, "kicker", classIs("label--accent")).textContent(); got != "a · b · c" {
					t.Errorf("kicker = %q, want the first three tags", got)
				}
				if got := doc.one(t, "toc head", classIs("toc-head")).one(t, "toc head count", classIs("meta")).textContent(); got != "1 section" {
					t.Errorf("toc head count = %q, want the singular", got)
				}
			}
			if card := doc.find(classIs("keep-item--index")); len(card) != 1 {
				t.Errorf("%d index cards, want 1", len(card))
			}
		})
	}
}

// checkErrorPage asserts the frame every status shares and returns the
// parsed page.
func checkErrorPage(t *testing.T, resp response, code int, method, path, label, heading, message string) *htmlNode {
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

	railLabel := main.one(t, "rail label", all(tagIs("p"), classIs("c-rail")))
	if got := railLabel.textContent(); got != "Error / "+label {
		t.Errorf("rail label = %q, want %q", got, "Error / "+label)
	}
	numeral := main.one(t, ".error-code", classIs("error-code"))
	if numeral.attr["aria-hidden"] != "true" || numeral.textContent() != strconv.Itoa(code) {
		t.Errorf("numeral = %q aria-hidden=%q", numeral.textContent(), numeral.attr["aria-hidden"])
	}
	h1 := doc.one(t, "h1", tagIs("h1"))
	if !h1.hasClass("error-title") || h1.textContent() != heading {
		t.Errorf("h1 = %q, want %q", h1.textContent(), heading)
	}
	status := strconv.Itoa(code)
	if code == http.StatusNotFound {
		status += " Not Found"
	}
	line := main.one(t, ".request-line", classIs("request-line"))
	if want := method + " " + path + " → " + status; line.textContent() != want {
		t.Errorf("request line = %q, want %q", line.textContent(), want)
	}
	if st := line.one(t, ".st", classIs("st")); st.textContent() != "→ "+status {
		t.Errorf("status span = %q", st.textContent())
	}
	if msg := main.one(t, ".error-msg", classIs("error-msg")); msg.textContent() != message {
		t.Errorf("message = %q, want %q", msg.textContent(), message)
	}

	cta := main.one(t, ".hero-cta", classIs("hero-cta"))
	var ways []string
	for _, c := range cta.find(func(n *htmlNode) bool { return n.tag == "a" || n.tag == "button" }) {
		ways = append(ways, c.tag+" "+c.attr["href"]+" "+c.textContent())
	}
	if want := []string{"a / Go home →", "a /blog Browse the writing", "button  Search ⌘K"}; !slices.Equal(ways, want) {
		t.Errorf("ways out = %q, want %q", ways, want)
	}
	if b := cta.one(t, "search button", tagIs("button")); b.attr["data-open-palette"] == "" || b.attr["type"] != "button" {
		t.Error("the search button does not open the palette")
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
			doc := checkErrorPage(t, resp, http.StatusNotFound, tt.method, tt.path, "Not found",
				"Nothing is listening on this route.", notFoundMessage)
			// The 404 heading carries the page's one redline.
			redlines := doc.find(classIs("redline"))
			if len(redlines) != 1 || redlines[0].tag != "em" || redlines[0].textContent() != "this route." || redlines[0].parent.tag != "h1" {
				t.Errorf("redline = %q, want one em “this route.” in the h1", texts(redlines))
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
		path  string
		code  int
		label string
	}{
		{"/boom", http.StatusInternalServerError, "Server error"},
		{"/panic", http.StatusInternalServerError, "Server error"},
		{"/unavailable", http.StatusServiceUnavailable, "Server error"},
		{"/teapot", http.StatusTeapot, "Request error"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			heading, message := errorText(tt.code)
			resp := request(t, app, http.MethodGet, tt.path)
			doc := checkErrorPage(t, resp, tt.code, http.MethodGet, tt.path, tt.label, heading, message)
			if strings.Contains(resp.body, "secret") {
				t.Error("error detail leaked to the client")
			}
			if n := len(doc.find(classIs("redline"))); n != 0 {
				t.Errorf("%d redlines on a %d page; only the 404 has one", n, tt.code)
			}
			if strings.Contains(resp.body, "this route") || strings.Contains(resp.body, "Not Found") {
				t.Error("404 copy on a non-404 page")
			}
		})
	}
}

// The requested path is printed back, so it must come out as text.
func TestErrorPageEscapesThePath(t *testing.T) {
	app := newApp(newRealSite(t).h)
	resp := request(t, app, http.MethodGet, `/x"><script>alert(1)</script>`)
	if resp.status != http.StatusNotFound {
		t.Fatalf("status = %d", resp.status)
	}
	if strings.Contains(resp.body, "<script>alert(1)") {
		t.Fatal("the path was written into the page unescaped")
	}
	line := parseHTML(t, resp.body).one(t, ".request-line", classIs("request-line"))
	if !strings.HasPrefix(line.textContent(), `GET /x"><script>alert(1)</script> →`) {
		t.Errorf("request line = %q", line.textContent())
	}
}

// The home page's shelf line names every project past the first three
// featured ones: a project without copy is just its name (no dangling
// comma), a description that repeats the title loses the repeat, and a
// leading article is lowercased to follow the comma.
func TestHomeShelf(t *testing.T) {
	projects := `[
	{"id": "a", "title": "Alpha", "description": "First project.", "featured": true},
	{"id": "b", "title": "Beta", "description": "Second project.", "featured": true},
	{"id": "c", "title": "Gamma", "description": "Third project.", "featured": true},
	{"id": "qr", "title": "pixelQR", "description": "pixelQR: QR code generation and management.", "featured": true},
	{"id": "ride", "title": "rideshare", "description": "An in-memory API. More.", "summary": "A carpooling API"},
	{"id": "solo", "title": "Solo"}
]`
	file := filepath.Join(t.TempDir(), "projects.json")
	if err := os.WriteFile(file, []byte(projects), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSite(t, realContent, file, realTemplates, models.DefaultConfig())
	resp := request(t, newApp(s.h), http.MethodGet, "/")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d", resp.status)
	}
	doc := parseHTML(t, resp.body)
	also := doc.one(t, ".also", classIs("also"))
	line := also.one(t, ".also p", tagIs("p")).textContent()
	// (the label is a block of its own: no space after it in the text)
	want := "Also on the shelfpixelQR, QR code generation and management · rideshare, a carpooling API · Solo"
	if line != want {
		t.Errorf("shelf line = %q, want %q", line, want)
	}
	if strings.Contains(resp.body, "<b>Solo</b>,") {
		t.Error("a project without copy leaves a dangling comma")
	}
	for _, p := range doc.find(all(tagIs("h3"), attrIs("id", "p-qr"))) {
		t.Errorf("pixelQR is featured past the first three, but has a work row: %q", p.textContent())
	}
}

// The projects page's "Featured" fact appears only when it says something:
// when not every project is featured.
func TestProjectsFacts(t *testing.T) {
	mixed := filepath.Join(t.TempDir(), "projects.json")
	if err := os.WriteFile(mixed, []byte(`[{"id": "a", "title": "A", "featured": true}, {"id": "b", "title": "B"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	allFeatured := filepath.Join(t.TempDir(), "projects.json")
	if err := os.WriteFile(allFeatured, []byte(`[{"id": "a", "title": "A", "featured": true}, {"id": "b", "title": "B", "featured": true}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		mixed:       "Projects2 | Featured1 | Everything elseGitHub ↗",
		allFeatured: "Projects2 | Everything elseGitHub ↗",
	} {
		s := newSite(t, realContent, file, realTemplates, models.DefaultConfig())
		resp := request(t, newApp(s.h), http.MethodGet, "/projects")
		if resp.status != http.StatusOK {
			t.Fatalf("status = %d", resp.status)
		}
		facts := parseHTML(t, resp.body).one(t, ".page-facts", classIs("page-facts"))
		var got []string
		for _, d := range facts.children(tagIs("div")) {
			got = append(got, d.textContent())
		}
		if strings.Join(got, " | ") != want {
			t.Errorf("facts = %q, want %q", strings.Join(got, " | "), want)
		}
	}
}
