package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/sagnikb/myspace/internal/blog"
	"github.com/sagnikb/myspace/internal/download"
	"github.com/sagnikb/myspace/internal/models"
	"github.com/sagnikb/myspace/internal/rss"
	"github.com/sagnikb/myspace/internal/search"
)

// pages are the templates rendered inside base.html.
var pages = []string{
	"home", "about", "projects", "blog_list", "blog_post",
	"tags", "tag_posts", "search", "contact", "error",
}

// pageHooks are the base.html blocks a page may redefine. A page must define
// "content"; any other name base.html defines is off limits to pages.
var pageHooks = []string{"header-extra", "main-class"}

// keepReadingSlots is the number of posts in a post's "Keep reading" list.
const keepReadingSlots = 3

type Handler struct {
	engine   *blog.Engine
	search   *search.Engine
	config   models.SiteConfig
	projects []models.Project
	tmpls    map[string]*template.Template
}

func New(engine *blog.Engine, searchEngine *search.Engine, config models.SiteConfig) *Handler {
	return &Handler{
		engine: engine,
		search: searchEngine,
		config: config,
	}
}

// LoadTemplates parses, for every page, base.html, every partials/*.html in
// name order (there may be none) and the page's own file into one template
// set. Partials hold {{define}}s shared by all pages. Because html/template
// silently lets a later definition replace an earlier one, a name defined by
// two partials, by a partial and base.html, or by a page and a partial is an
// error, as is a page redefining a base.html template other than the hooks in
// pageHooks. Each set is escaped once here, so a contextual-escaping mistake
// also fails at startup rather than on the first request.
func (h *Handler) LoadTemplates(dir string) error {
	funcMap := template.FuncMap{
		"join":     strings.Join,
		"html":     func(s string) template.HTML { return template.HTML(s) },
		"truncate": truncate,
		"add":      func(a, b int) int { return a + b },
		"split": func(s, sep string) []string {
			if s == "" {
				return nil
			}
			parts := strings.Split(s, sep)
			var result []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					result = append(result, p)
				}
			}
			return result
		},
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i
			}
			return s
		},
		"asset":       h.assetURL,
		"currentYear": func() int { return time.Now().Year() },
		"lower":       strings.ToLower,
		"dict":        dict,
		// pathEscape makes a value one URL path segment: a tag such as
		// "ci/cd" or "c#" links to /tags/ci%2Fcd, /tags/c%23 (html/template
		// leaves "/", "?" and "#" alone in a URL). The handlers unescape it.
		"pathEscape": url.PathEscape,
	}

	baseFile := filepath.Join(dir, "base.html")
	partials, err := filepath.Glob(filepath.Join(dir, "partials", "*.html"))
	if err != nil {
		return fmt.Errorf("list partials: %w", err)
	}
	slices.Sort(partials)

	// owner maps every name defined in base.html or a partial to its file.
	owner := make(map[string]string)
	baseNames, err := definedNames(funcMap, baseFile)
	if err != nil {
		return err
	}
	for _, name := range baseNames {
		owner[name] = baseFile
	}
	for _, file := range partials {
		names, err := definedNames(funcMap, file)
		if err != nil {
			return err
		}
		for _, name := range names {
			if prev, ok := owner[name]; ok {
				return fmt.Errorf("template %q is defined in both %s and %s", name, prev, file)
			}
			owner[name] = file
		}
	}

	tmpls := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		pageFile := filepath.Join(dir, page+".html")
		names, err := definedNames(funcMap, pageFile)
		if err != nil {
			return err
		}
		if !slices.Contains(names, "content") {
			return fmt.Errorf("%s does not define \"content\"", pageFile)
		}
		for _, name := range names {
			prev, ok := owner[name]
			if !ok || (prev == baseFile && slices.Contains(pageHooks, name)) {
				continue
			}
			if prev == baseFile {
				return fmt.Errorf("%s redefines %q from %s; pages may only define \"content\", the hooks %s and names of their own",
					pageFile, name, baseFile, strings.Join(pageHooks, ", "))
			}
			return fmt.Errorf("template %q is defined in both %s and %s", name, prev, pageFile)
		}

		files := append(append([]string{baseFile}, partials...), pageFile)
		tmpl, err := template.New("").Funcs(funcMap).ParseFiles(files...)
		if err != nil {
			return fmt.Errorf("parse template %s: %w", page, err)
		}
		// html/template escapes a set the first time it executes. Running it
		// once without data surfaces escaping errors now; the errors that the
		// missing data causes are expected and ignored.
		for _, entry := range []string{"base", "main-class"} {
			if tmpl.Lookup(entry) == nil {
				continue
			}
			if err := tmpl.ExecuteTemplate(io.Discard, entry, nil); err != nil {
				var escapeErr *template.Error
				if errors.As(err, &escapeErr) {
					return fmt.Errorf("template %s: %w", page, err)
				}
			}
		}
		tmpls[page] = tmpl
	}
	h.tmpls = tmpls
	return nil
}

// definedNames returns the names file defines with {{define}} or {{block}}.
func definedNames(funcMap template.FuncMap, file string) ([]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}
	root := filepath.Base(file)
	t, err := template.New(root).Funcs(funcMap).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	var names []string
	for _, d := range t.Templates() {
		if d.Name() != root {
			names = append(names, d.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

// dict builds a map from alternating string keys and values, so a partial
// can take several arguments: {{template "x" dict "Post" . "Tag" $.Tag}}.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments (%d)", len(pairs))
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %d is %T, want string", i/2, pairs[i])
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

func (h *Handler) LoadProjects(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read projects: %w", err)
	}
	if err := json.Unmarshal(data, &h.projects); err != nil {
		return fmt.Errorf("parse projects: %w", err)
	}
	return nil
}

// assetURL returns the URL of a file under static/ with the cache-busting
// version appended.
func (h *Handler) assetURL(path string) string {
	u := "/static/" + strings.TrimPrefix(path, "/")
	if h.config.AssetVersion != "" {
		u += "?v=" + h.config.AssetVersion
	}
	return u
}

func (h *Handler) Robots(c *fiber.Ctx) error {
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /\n")
	b.WriteString("Disallow: /search\n")
	b.WriteString("Disallow: /blog/*/download/\n\n")
	fmt.Fprintf(&b, "Sitemap: %s/sitemap.xml\n", h.config.BaseURL)
	c.Set("Content-Type", "text/plain; charset=utf-8")
	return c.SendString(b.String())
}

// render executes a page template inside base.html. Failures are returned to
// the error handler, which logs them and serves a generic error page, so
// template details never reach the client.
func (h *Handler) render(c *fiber.Ctx, name string, data fiber.Map) error {
	tmpl, ok := h.tmpls[name]
	if !ok {
		return fmt.Errorf("render: unknown template %q", name)
	}
	if data == nil {
		data = fiber.Map{}
	}
	data["Config"] = h.config
	data["CurrentPath"] = c.Path()
	data["Page"] = name
	if _, ok := data["Section"]; !ok {
		data["Section"] = ""
	}
	if _, ok := data["NoIndex"]; !ok {
		data["NoIndex"] = false
	}
	// <main> gets a class attribute only when the page defines "main-class"
	data["MainClass"] = ""
	if tmpl.Lookup("main-class") != nil {
		var class bytes.Buffer
		if err := tmpl.ExecuteTemplate(&class, "main-class", data); err != nil {
			return fmt.Errorf("render %s: main-class: %w", name, err)
		}
		data["MainClass"] = strings.Join(strings.Fields(html.UnescapeString(class.String())), " ")
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", data); err != nil {
		return fmt.Errorf("render %s: %w", name, err)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return c.Send(buf.Bytes())
}

// Route handlers

func (h *Handler) Home(c *fiber.Ctx) error {
	featured, other := h.splitProjects()
	tags := h.engine.GetAllTags()
	return h.render(c, "home", fiber.Map{
		"Title":            "Home",
		"Description":      h.config.Description,
		"Section":          "home",
		"LatestBlogs":      h.engine.GetLatestBlogs(3),
		"PullquotePost":    h.engine.GetPullquoteBlog(),
		"FeaturedProjects": featured,
		"OtherProjects":    other,
		"PostCount":        len(h.engine.GetAllBlogs()),
		"ProjectCount":     len(h.projects),
		"TagCount":         len(tags),
		"Tags":             tags,
		"SchemaJSON":       ldJSON(h.websiteSchema()),
	})
}

func (h *Handler) About(c *fiber.Ctx) error {
	return h.render(c, "about", fiber.Map{
		"Title":       "About",
		"Description": fmt.Sprintf("About %s — software engineer building backend systems and developer tools in Go.", h.config.Author),
		"Section":     "about",
		"PostCount":   len(h.engine.GetAllBlogs()),
		"SchemaJSON":  ldJSON(h.personSchema()),
	})
}

func (h *Handler) Projects(c *fiber.Ctx) error {
	featured, other := h.splitProjects()
	return h.render(c, "projects", fiber.Map{
		"Title":            "Projects",
		"Description":      fmt.Sprintf("Open source projects and tools built by %s.", h.config.Author),
		"Section":          "projects",
		"Projects":         h.projects,
		"FeaturedProjects": featured,
		"OtherProjects":    other,
	})
}

func (h *Handler) BlogList(c *fiber.Ctx) error {
	blogs := h.engine.GetAllBlogs()
	return h.render(c, "blog_list", fiber.Map{
		"Title":       "Writing",
		"Description": blogDescription,
		"Section":     "blog",
		"Blogs":       blogs,
		"Years":       h.engine.GetBlogsByYear(),
		"Tags":        h.engine.GetAllTags(),
		"SchemaJSON":  ldJSON(h.blogSchema(blogs)),
	})
}

func (h *Handler) BlogPost(c *fiber.Ctx) error {
	slug, ok := param(c, "slug")
	if !ok {
		return h.NotFound(c)
	}
	post := h.engine.GetBlog(slug)
	if post == nil {
		return h.NotFound(c)
	}

	newer, older := h.engine.GetAdjacentBlogs(slug)
	// Up to two suggestions can be the adjacent posts, which are already shown.
	suggested := h.engine.GetSuggestedBlogs(slug, keepReadingSlots+2)
	return h.render(c, "blog_post", fiber.Map{
		"Title":        post.Title,
		"Description":  post.Description,
		"Section":      "blog",
		"Post":         post,
		"RelatedPosts": h.engine.GetRelatedBlogs(slug, 3),
		"NewerPost":    newer,
		"OlderPost":    older,
		"KeepReading":  keepReading(post, older, newer, suggested),
		"SchemaJSON":   ldJSON(h.postingSchema(post), h.breadcrumbSchema(post)),
	})
}

// keepReading fills post's "Keep reading" slots in order: the next older
// post (prev), the next newer post (next), then suggestions not already
// listed. A suggestion that shares a tag with post is "related"; the rest
// (the newest posts, when too few share a tag) are just "more" writing.
func keepReading(post, older, newer *models.Blog, suggested []*models.Blog) []models.KeepItem {
	items := make([]models.KeepItem, 0, keepReadingSlots)
	listed := func(p *models.Blog) bool {
		for _, it := range items {
			if it.Post.Slug == p.Slug {
				return true
			}
		}
		return false
	}
	if older != nil {
		items = append(items, models.KeepItem{Dir: "prev", Post: older})
	}
	if newer != nil && !listed(newer) {
		items = append(items, models.KeepItem{Dir: "next", Post: newer})
	}
	for _, p := range suggested {
		if len(items) == keepReadingSlots {
			break
		}
		if p != nil && !listed(p) {
			dir := "more"
			if sharesTag(post, p) {
				dir = "related"
			}
			items = append(items, models.KeepItem{Dir: dir, Post: p})
		}
	}
	return items
}

// sharesTag reports whether a and b have a tag in common.
func sharesTag(a, b *models.Blog) bool {
	if a == nil || b == nil {
		return false
	}
	for _, t := range a.Tags {
		if slices.Contains(b.Tags, t) {
			return true
		}
	}
	return false
}

func (h *Handler) Tags(c *fiber.Ctx) error {
	return h.render(c, "tags", fiber.Map{
		"Title":     "Topics",
		"Section":   "blog",
		"Tags":      h.engine.GetAllTags(),
		"PostCount": len(h.engine.GetAllBlogs()),
	})
}

func (h *Handler) TagPosts(c *fiber.Ctx) error {
	tag, ok := param(c, "tag")
	if !ok {
		return h.NotFound(c)
	}
	blogs := h.engine.GetBlogsByTag(tag)
	if len(blogs) == 0 {
		return h.NotFound(c)
	}

	return h.render(c, "tag_posts", fiber.Map{
		"Title":   "#" + tag,
		"Section": "blog",
		"Tag":     tag,
		"Blogs":   blogs,
	})
}

func (h *Handler) Search(c *fiber.Ctx) error {
	query := strings.TrimSpace(c.Query("q"))
	results, err := h.search.Search(query)
	if err != nil {
		return err
	}

	hits := make([]models.SearchHit, 0, len(results))
	for _, r := range results {
		post := h.engine.GetBlog(r.Slug)
		if post == nil {
			continue // stale index row for a removed or unpublished post
		}
		hits = append(hits, models.SearchHit{Post: post, TitleHTML: r.TitleHTML, Snippet: r.Snippet})
	}

	title := "Search"
	if query != "" {
		title = "Search: " + query
	}
	return h.render(c, "search", fiber.Map{
		"Title":       title,
		"Section":     "blog",
		"NoIndex":     true,
		"Query":       query,
		"Results":     hits,
		"ResultCount": len(hits),
		"PostCount":   len(h.engine.GetAllBlogs()),
	})
}

func (h *Handler) Contact(c *fiber.Ctx) error {
	return h.render(c, "contact", fiber.Map{
		"Title":   "Contact",
		"Section": "contact",
	})
}

// Favicon redirects /favicon.ico to the site icon (Config.Favicon).
func (h *Handler) Favicon(c *fiber.Ctx) error {
	if h.config.Favicon == "" {
		return h.NotFound(c)
	}
	return c.Redirect("/static/"+h.config.Favicon, fiber.StatusMovedPermanently)
}

func (h *Handler) RSS(c *fiber.Ctx) error {
	blogs := h.engine.GetAllBlogs()
	data, err := rss.Generate(blogs, h.config)
	if err != nil {
		return fmt.Errorf("generate rss: %w", err)
	}
	c.Set("Content-Type", "application/rss+xml; charset=utf-8")
	return c.Send(data)
}

func (h *Handler) Sitemap(c *fiber.Ctx) error {
	blogs := h.engine.GetAllBlogs()
	tags := h.engine.GetAllTags()

	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")

	type sitemapEntry struct {
		loc        string
		changefreq string
		priority   string
	}
	staticPages := []sitemapEntry{
		{"/", "weekly", "1.0"},
		{"/blog", "daily", "0.9"},
		{"/projects", "monthly", "0.8"},
		{"/about", "monthly", "0.7"},
		{"/tags", "weekly", "0.6"},
		{"/contact", "yearly", "0.5"},
	}
	for _, p := range staticPages {
		fmt.Fprintf(&b, "<url><loc>%s</loc><changefreq>%s</changefreq><priority>%s</priority></url>\n",
			html.EscapeString(h.config.BaseURL+p.loc), p.changefreq, p.priority)
	}
	for _, blog := range blogs {
		fmt.Fprintf(&b, "<url><loc>%s</loc><lastmod>%s</lastmod><changefreq>monthly</changefreq><priority>0.8</priority></url>\n",
			html.EscapeString(h.config.BaseURL+"/blog/"+url.PathEscape(blog.Slug)), blog.Date.Format("2006-01-02"))
	}
	for _, tag := range tags {
		fmt.Fprintf(&b, "<url><loc>%s</loc><changefreq>weekly</changefreq><priority>0.5</priority></url>\n",
			html.EscapeString(h.config.BaseURL+"/tags/"+url.PathEscape(tag.Name)))
	}

	b.WriteString("</urlset>")
	c.Set("Content-Type", "application/xml; charset=utf-8")
	return c.SendString(b.String())
}

func (h *Handler) DownloadPDF(c *fiber.Ctx) error {
	post := h.downloadPost(c)
	if post == nil {
		return h.NotFound(c)
	}

	data, err := download.GeneratePDF(post, h.config)
	if err != nil {
		return fmt.Errorf("generate pdf %s: %w", post.Slug, err)
	}

	c.Set("Content-Type", "application/pdf")
	c.Set("Content-Disposition", attachment(post.Slug+".pdf"))
	return c.Send(data)
}

func (h *Handler) DownloadEPUB(c *fiber.Ctx) error {
	post := h.downloadPost(c)
	if post == nil {
		return h.NotFound(c)
	}

	data, err := download.GenerateEPUB(post)
	if err != nil {
		return fmt.Errorf("generate epub %s: %w", post.Slug, err)
	}

	c.Set("Content-Type", "application/epub+zip")
	c.Set("Content-Disposition", attachment(post.Slug+".epub"))
	return c.Send(data)
}

func (h *Handler) downloadPost(c *fiber.Ctx) *models.Blog {
	slug, ok := param(c, "slug")
	if !ok {
		return nil
	}
	return h.engine.GetBlog(slug)
}

func (h *Handler) splitProjects() (featured, other []models.Project) {
	for _, p := range h.projects {
		if p.Featured {
			featured = append(featured, p)
		} else {
			other = append(other, p)
		}
	}
	return featured, other
}

// param returns the URL-decoded route parameter name, reporting false when it
// is empty or malformed.
func param(c *fiber.Ctx, name string) (string, bool) {
	v, err := url.PathUnescape(c.Params(name))
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}

func attachment(filename string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": filename})
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:max(n, 0)]) + "..."
}
