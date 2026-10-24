package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/sagnikb/myspace/internal/blog"
	"github.com/sagnikb/myspace/internal/database"
	"github.com/sagnikb/myspace/internal/models"
	"github.com/sagnikb/myspace/internal/search"
)

const (
	realContent   = "../../content/blogs"
	realProjects  = "../../content/projects/projects.json"
	realTemplates = "../../templates"
	realStatic    = "../../static"
)

type site struct {
	h      *Handler
	engine *blog.Engine
	db     *database.DB
}

// newSite wires a handler like main does, backed by a temporary database.
func newSite(t *testing.T, contentDir, projectsFile, templatesDir string, config models.SiteConfig) site {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	engine := blog.NewEngine(db)
	if err := engine.LoadFromDir(contentDir); err != nil {
		t.Fatal(err)
	}
	h := New(engine, search.New(db), config)
	if err := h.LoadTemplates(templatesDir); err != nil {
		t.Fatal(err)
	}
	if err := h.LoadProjects(projectsFile); err != nil {
		t.Fatal(err)
	}
	return site{h: h, engine: engine, db: db}
}

func newRealSite(t *testing.T) site {
	t.Helper()
	config := models.DefaultConfig()
	config.AssetVersion = "testver"
	return newSite(t, realContent, realProjects, realTemplates, config)
}

func newApp(h *Handler, before ...func(*fiber.App)) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: h.ErrorHandler})
	for _, f := range before {
		f(app)
	}
	h.Register(app, realStatic)
	return app
}

type response struct {
	status int
	header http.Header
	body   string
}

// get sends a GET for target, which is used verbatim as the request URI so
// tests can send malformed escapes.
func get(t *testing.T, app *fiber.App, target string) response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RequestURI = target
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func TestMainRoutes(t *testing.T) {
	s := newRealSite(t)
	app := newApp(s.h)

	type route struct{ path, ctype string }
	routes := []route{
		{"/", "text/html"},
		{"/about", "text/html"},
		{"/projects", "text/html"},
		{"/blog", "text/html"},
		{"/tags", "text/html"},
		{"/search", "text/html"},
		{"/search?q=go", "text/html"},
		{"/contact", "text/html"},
		{"/rss.xml", "application/rss+xml"},
		{"/sitemap.xml", "application/xml"},
		{"/robots.txt", "text/plain"},
		{"/search-index.json", "application/json"},
		{"/static/css/style.css", "text/css"},
	}
	for _, b := range s.engine.GetAllBlogs() {
		routes = append(routes,
			route{"/blog/" + b.Slug, "text/html"},
			route{"/blog/" + b.Slug + "/download/pdf", "application/pdf"},
			route{"/blog/" + b.Slug + "/download/epub", "application/epub+zip"},
		)
	}
	for _, tag := range s.engine.GetAllTags() {
		routes = append(routes, route{"/tags/" + url.PathEscape(tag.Name), "text/html"})
	}

	for _, r := range routes {
		t.Run(r.path, func(t *testing.T) {
			resp := get(t, app, r.path)
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %.300s", resp.status, resp.body)
			}
			if ct := resp.header.Get("Content-Type"); !strings.HasPrefix(ct, r.ctype) {
				t.Errorf("Content-Type = %q, want %s", ct, r.ctype)
			}
			if resp.body == "" {
				t.Error("empty body")
			}
		})
	}
}

func TestNotFoundPages(t *testing.T) {
	app := newApp(newRealSite(t).h)
	paths := []string{
		"/nope",
		"/blog/nope",
		"/tags/nope",
		"/blog/nope/download/pdf",
		"/blog/nope/download/epub",
		"/blog/%ZZ",
		"/static/nope.css",
		"/deeply/nested/unknown/path",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			resp := get(t, app, path)
			if resp.status != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", resp.status)
			}
			if ct := resp.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", ct)
			}
			if !strings.Contains(strings.ToLower(resp.body), "<html") {
				t.Errorf("body is not an HTML page: %.200s", resp.body)
			}
		})
	}
}

// The site icon follows Config.Favicon: the generated SVG by default, the
// owner's favicon.ico when main finds one in static/.
func TestFavicon(t *testing.T) {
	iconRe := regexp.MustCompile(`<link rel="icon"[^>]*>`)
	tests := []struct {
		name, favicon, ftype string
		location, link       string // link "" means no icon link in <head>
	}{
		// html/template writes the "+" of image/svg+xml as &#43; in attributes.
		{"default svg", "img/favicon.svg", "image/svg+xml",
			"/static/img/favicon.svg", `<link rel="icon" href="/static/img/favicon.svg?v=testver" type="image/svg&#43;xml">`},
		{"owner ico", "favicon.ico", "image/x-icon",
			"/static/favicon.ico", `<link rel="icon" href="/static/favicon.ico?v=testver" type="image/x-icon">`},
		{"none", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := models.DefaultConfig()
			config.AssetVersion = "testver"
			config.Favicon, config.FaviconType = tt.favicon, tt.ftype
			app := newApp(newSite(t, realContent, realProjects, realTemplates, config).h)

			resp := get(t, app, "/favicon.ico")
			if tt.location == "" {
				if resp.status != http.StatusNotFound {
					t.Errorf("status = %d, want 404", resp.status)
				}
			} else if resp.status != http.StatusMovedPermanently || resp.header.Get("Location") != tt.location {
				t.Errorf("got %d to %q, want 301 to %q", resp.status, resp.header.Get("Location"), tt.location)
			}

			home := get(t, app, "/").body
			// the generated touch icon only accompanies the generated favicon
			if touch := strings.Contains(home, `<link rel="apple-touch-icon" href="/static/img/apple-touch-icon.png?v=testver">`); touch != (tt.favicon != "favicon.ico") {
				t.Errorf("apple-touch-icon linked = %v with favicon %q", touch, tt.favicon)
			}
			links := iconRe.FindAllString(home, -1)
			if tt.link == "" {
				if len(links) != 0 {
					t.Errorf("icon links = %q, want none", links)
				}
			} else if len(links) != 1 || links[0] != tt.link {
				t.Errorf("icon links = %q, want [%s]", links, tt.link)
			}
		})
	}
}

// /workwatch/* serves static/workwatch (the published installer URLs) and
// falls through to the 404 page when the directory or file is missing.
func TestWorkwatchStatic(t *testing.T) {
	s := newRealSite(t)
	withFiles := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withFiles, "workwatch", "releases"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"workwatch/install.sh":           "#!/bin/bash\necho install\n",
		"workwatch/releases/ww.tar.gz":   "tarball",
		"workwatch/releases/ww.tar.gz.x": "sum",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(withFiles, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	appFor := func(staticDir string) *fiber.App {
		app := fiber.New(fiber.Config{ErrorHandler: s.h.ErrorHandler})
		s.h.Register(app, staticDir)
		return app
	}

	app := appFor(withFiles)
	for name, body := range files {
		resp := get(t, app, "/"+name)
		if resp.status != http.StatusOK || resp.body != body {
			t.Errorf("GET /%s = %d %.80q, want 200 %q", name, resp.status, resp.body, body)
		}
		if cc := resp.header.Get("Cache-Control"); cc != "public, max-age=3600" {
			t.Errorf("GET /%s Cache-Control = %q", name, cc)
		}
	}
	for _, c := range []struct {
		app  *fiber.App
		path string
	}{
		{app, "/workwatch/nope.sh"},
		{appFor(t.TempDir()), "/workwatch/install.sh"},
	} {
		resp := get(t, c.app, c.path)
		if resp.status != http.StatusNotFound || !strings.Contains(resp.body, "<html") {
			t.Errorf("GET %s = %d %.80q, want the 404 page", c.path, resp.status, resp.body)
		}
	}
}

func TestSearchIndexJSON(t *testing.T) {
	s := newRealSite(t)
	resp := get(t, newApp(s.h), "/search-index.json")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d", resp.status)
	}
	if cc := resp.header.Get("Cache-Control"); cc != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", cc)
	}

	var raw struct {
		Posts    []map[string]json.RawMessage `json:"posts"`
		Projects []map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal([]byte(resp.body), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	assertKeys := func(kind string, obj map[string]json.RawMessage, keys ...string) {
		t.Helper()
		if len(obj) != len(keys) {
			t.Errorf("%s has keys %v, want exactly %v", kind, obj, keys)
		}
		for _, k := range keys {
			if _, ok := obj[k]; !ok {
				t.Errorf("%s missing key %q", kind, k)
			}
		}
	}
	for _, p := range raw.Posts {
		assertKeys("post", p, "title", "slug", "url", "description", "tags", "date", "readingTime")
		if !strings.HasPrefix(string(p["tags"]), "[") {
			t.Errorf("post tags = %s, want an array", p["tags"])
		}
	}
	for _, p := range raw.Projects {
		assertKeys("project", p, "id", "title", "description", "summary", "tech", "github", "demo")
		if !strings.HasPrefix(string(p["tech"]), "[") {
			t.Errorf("project tech = %s, want an array", p["tech"])
		}
	}

	var idx searchIndex
	if err := json.Unmarshal([]byte(resp.body), &idx); err != nil {
		t.Fatal(err)
	}
	blogs := s.engine.GetAllBlogs()
	if len(idx.Posts) != len(blogs) || len(idx.Projects) != len(s.h.projects) {
		t.Fatalf("got %d posts / %d projects, want %d / %d", len(idx.Posts), len(idx.Projects), len(blogs), len(s.h.projects))
	}
	for i, p := range idx.Posts {
		if p.Slug != blogs[i].Slug {
			t.Errorf("posts[%d] = %s, want %s (newest first)", i, p.Slug, blogs[i].Slug)
		}
		if p.URL != "/blog/"+p.Slug {
			t.Errorf("url = %q", p.URL)
		}
		if _, err := time.Parse("2006-01-02", p.Date); err != nil {
			t.Errorf("date = %q: %v", p.Date, err)
		}
		if p.ReadingTime < 1 || p.Title == "" {
			t.Errorf("incomplete post %+v", p)
		}
	}
}

func TestSearchQueries(t *testing.T) {
	s := newRealSite(t)
	app := newApp(s.h)
	queries := []string{
		"c++", `"`, `""`, "'", "(", ")", "*", ":", "-", "^", "AND", "NEAR(", "foo:bar",
		"title:docker", "e-mail", "日本語", "naïve", "%", strings.Repeat("long ", 300),
		"<script>alert(1)</script>",
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			resp := get(t, app, "/search?q="+url.QueryEscape(q))
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.status)
			}
			if strings.Contains(resp.body, "<script>alert(1)</script>") {
				t.Error("query reflected unescaped")
			}
		})
	}

	// Every real post is found, highlighted, by its own title.
	for _, b := range s.engine.GetAllBlogs() {
		resp := get(t, app, "/search?q="+url.QueryEscape(b.Title))
		if !strings.Contains(resp.body, "/blog/"+b.Slug) || !strings.Contains(resp.body, "<mark>") {
			t.Errorf("search for %q does not return %s with highlighted matches", b.Title, b.Slug)
		}
	}
}

func TestErrorHandler(t *testing.T) {
	h := newRealSite(t).h
	app := newApp(h, func(app *fiber.App) {
		app.Use(recover.New())
		app.Get("/boom", func(c *fiber.Ctx) error { return errors.New("secret database detail") })
		app.Get("/panic", func(c *fiber.Ctx) error { panic("secret panic detail") })
		app.Get("/teapot", func(c *fiber.Ctx) error { return fiber.NewError(http.StatusTeapot, "secret teapot detail") })
	})

	tests := []struct {
		path   string
		status int
	}{
		{"/boom", http.StatusInternalServerError},
		{"/panic", http.StatusInternalServerError},
		{"/teapot", http.StatusTeapot},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp := get(t, app, tt.path)
			if resp.status != tt.status {
				t.Fatalf("status = %d, want %d", resp.status, tt.status)
			}
			if !strings.HasPrefix(resp.header.Get("Content-Type"), "text/html") {
				t.Errorf("Content-Type = %q", resp.header.Get("Content-Type"))
			}
			if strings.Contains(resp.body, "secret") {
				t.Errorf("error detail leaked to the client: %.300s", resp.body)
			}
		})
	}
}

// writeTemplates creates a template set where every page prints contract
// fields; overrides replace or add files (paths relative to the templates
// directory, e.g. "partials/x.html").
func writeTemplates(t *testing.T, overrides map[string]string) string {
	t.Helper()
	files := map[string]string{
		"base.html":      `{{define "base"}}page={{.Page}};title={{.Title}};section={{.Section}};noindex={{.NoIndex}};path={{.CurrentPath}};asset={{asset "css/style.css"}};year={{currentYear}};{{template "content" .}}{{end}}`,
		"home.html":      `{{define "content"}}posts={{.PostCount}};projects={{.ProjectCount}};tagcount={{.TagCount}};tags={{range .Tags}}{{.Name}}:{{.Count}},{{end}};latest={{range .LatestBlogs}}{{.Slug}},{{end}};featured={{range .FeaturedProjects}}{{.ID}}:{{.Figure}},{{end}};other={{range .OtherProjects}}{{.ID}}:{{.ShortSummary}},{{end}}{{end}}`,
		"about.html":     `{{define "content"}}about;posts={{.PostCount}}{{end}}`,
		"projects.html":  `{{define "content"}}all={{range .Projects}}{{.ID}},{{end}};featured={{range .FeaturedProjects}}{{.ID}},{{end}};other={{range .OtherProjects}}{{.ID}},{{end}}{{end}}`,
		"blog_list.html": `{{define "content"}}blogs={{range .Blogs}}{{.Slug}},{{end}};years={{range .Years}}{{.Year}}:{{range .Posts}}{{.Slug}},{{end}}|{{end}};tags={{len .Tags}}{{end}}`,
		"blog_post.html": `{{define "content"}}post={{.Post.Slug}};newer={{with .NewerPost}}{{.Slug}}{{else}}none{{end}};older={{with .OlderPost}}{{.Slug}}{{else}}none{{end}};related={{range .RelatedPosts}}{{.Slug}},{{end}};keep={{range .KeepReading}}{{.Dir}}:{{.Post.Slug}},{{end}};toc={{range .Post.TOC}}{{.Level}}#{{.ID}}#{{.Title}},{{end}};schema={{if .SchemaJSON}}yes{{end}}{{end}}`,
		"tags.html":      `{{define "content"}}tags={{range .Tags}}{{.Name}}:{{.Count}},{{end}};posts={{.PostCount}}{{end}}`,
		"tag_posts.html": `{{define "content"}}tag={{.Tag}};blogs={{range .Blogs}}{{.Slug}},{{end}}{{end}}`,
		"search.html":    `{{define "content"}}query={{.Query}};count={{.ResultCount}};posts={{.PostCount}};hits={{range .Results}}{{.Post.Slug}}|{{.TitleHTML}}|{{.Snippet}};{{end}}{{end}}`,
		"contact.html":   `{{define "content"}}contact{{end}}`,
		"error.html":     `{{define "content"}}code={{.Code}};heading={{.Heading}};message={{.Message}};method={{.Method}}{{end}}`,
	}
	for name, content := range overrides {
		files[name] = content
	}
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func writeContent(t *testing.T) (contentDir, projectsFile string) {
	t.Helper()
	contentDir = t.TempDir()
	posts := map[string]string{
		"newest.md": "---\ntitle: \"Newest Post\"\ndate: 2026-03-01\ntags: [\"go\", \"web\"]\n---\n\n## Intro\n\nUses iptables daily.\n\n```bash\n# Not a heading\nsudo sysctl -w net.ipv4.ip_forward=1\n```\n\n### Details\n\nMore.\n",
		"middle.md": "---\ntitle: \"Middle Post\"\ndate: 2026-02-01\ntags: [\"go\"]\n---\n\nBody.\n",
		"oldest.md": "---\ntitle: \"Oldest Post\"\ndate: 2025-06-01\ntags: [\"rust\", \"web dev\"]\n---\n\nBody.\n",
	}
	for name, src := range posts {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	projectsFile = filepath.Join(t.TempDir(), "projects.json")
	projects := `[{"id":"p1","title":"P1","featured":true,"figure":"workwatch"},{"id":"p2","title":"P2","description":"Second project. With more text."},{"id":"p3","title":"P3","featured":true,"tech_stack":["Go"]}]`
	if err := os.WriteFile(projectsFile, []byte(projects), 0o644); err != nil {
		t.Fatal(err)
	}
	return contentDir, projectsFile
}

func TestTemplateData(t *testing.T) {
	contentDir, projectsFile := writeContent(t)
	config := models.DefaultConfig()
	config.AssetVersion = "testver"
	s := newSite(t, contentDir, projectsFile, writeTemplates(t, nil), config)

	// A stale index row (post no longer published) must never surface.
	if err := s.db.IndexBlog(models.Blog{Slug: "ghost", Title: "Ghost", PlainText: "iptables everywhere"}); err != nil {
		t.Fatal(err)
	}
	app := newApp(s.h)
	year := strconv.Itoa(time.Now().Year())

	tests := []struct {
		path   string
		status int
		want   []string
	}{
		{"/", 200, []string{
			"page=home;title=Home;section=home;noindex=false;path=/;asset=/static/css/style.css?v=testver;year=" + year + ";",
			"posts=3;projects=3;tagcount=4;tags=go:2,rust:1,web:1,web dev:1,;latest=newest,middle,oldest,;featured=p1:workwatch,p3:,;other=p2:Second project,",
		}},
		{"/about", 200, []string{"page=about;title=About;section=about;", "about;posts=3"}},
		{"/contact", 200, []string{"page=contact;title=Contact;section=contact;"}},
		{"/projects", 200, []string{"page=projects;title=Projects;section=projects;", "all=p1,p2,p3,;featured=p1,p3,;other=p2,"}},
		{"/blog", 200, []string{"page=blog_list;title=Writing;section=blog;", "blogs=newest,middle,oldest,;years=2026:newest,middle,|2025:oldest,|;tags=4"}},
		{"/blog/newest", 200, []string{"page=blog_post;title=Newest Post;section=blog;", "post=newest;newer=none;older=middle;related=middle,;keep=prev:middle,more:oldest,;toc=2#intro#Intro,3#details#Details,;schema=yes"}},
		{"/blog/middle", 200, []string{"post=middle;newer=newest;older=oldest;related=newest,;keep=prev:oldest,next:newest,;"}},
		{"/blog/oldest", 200, []string{"post=oldest;newer=middle;older=none;related=;keep=next:middle,more:newest,;toc=;"}},
		{"/tags", 200, []string{"page=tags;title=Topics;section=blog;", "tags=go:2,rust:1,web:1,web dev:1,;posts=3"}},
		{"/tags/go", 200, []string{"page=tag_posts;title=#go;section=blog;", "tag=go;blogs=newest,middle,"}},
		{"/tags/web%20dev", 200, []string{"title=#web dev;", "tag=web dev;blogs=oldest,"}},
		{"/search?q=iptables", 200, []string{"page=search;title=Search: iptables;section=blog;noindex=true;", "query=iptables;count=1;posts=3;hits=newest|Newest Post|", "<mark>iptables</mark>"}},
		// a term only in code finds the post, which quotes its prose, not the listing
		{"/search?q=sysctl", 200, []string{"count=1;posts=3;hits=newest|Newest Post|Intro\nUses iptables daily."}},
		{"/search?q=%2B%2B", 200, []string{"query=&#43;&#43;;count=0;posts=3;hits="}},
		{"/search", 200, []string{"page=search;title=Search;", "noindex=true;", "query=;count=0;posts=3;"}},
		{"/nope", 404, []string{"page=error;title=Page not found;section=;noindex=true;", "code=404;heading=Page not found;", "method=GET"}},
		{"/tags/unknown", 404, []string{"code=404;", "method=GET"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp := get(t, app, tt.path)
			if resp.status != tt.status {
				t.Fatalf("status = %d, want %d; body: %s", resp.status, tt.status, resp.body)
			}
			for _, want := range tt.want {
				if !strings.Contains(resp.body, want) {
					t.Errorf("body missing %q\nbody: %s", want, resp.body)
				}
			}
		})
	}
}

func TestRenderFailureDoesNotLeak(t *testing.T) {
	contentDir, projectsFile := writeContent(t)
	broken := `{{define "content"}}{{index .Nope 3}}{{end}}`

	tests := []struct {
		name      string
		overrides map[string]string
		ctype     string
		want      string
	}{
		{"error page rendered", map[string]string{"home.html": broken}, "text/html", "code=500;heading=Something went wrong;"},
		{"plain text fallback", map[string]string{"home.html": broken, "error.html": broken}, "text/plain", "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSite(t, contentDir, projectsFile, writeTemplates(t, tt.overrides), models.DefaultConfig())
			resp := get(t, newApp(s.h), "/")
			if resp.status != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", resp.status)
			}
			if !strings.HasPrefix(resp.header.Get("Content-Type"), tt.ctype) {
				t.Errorf("Content-Type = %q, want %s", resp.header.Get("Content-Type"), tt.ctype)
			}
			if !strings.Contains(resp.body, tt.want) {
				t.Errorf("body = %q, want it to contain %q", resp.body, tt.want)
			}
			if strings.Contains(resp.body, "untyped nil") || strings.Contains(resp.body, "home.html") {
				t.Errorf("template error leaked: %q", resp.body)
			}
		})
	}
}

var ldJSONRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

func TestJSONLD(t *testing.T) {
	s := newRealSite(t)
	app := newApp(s.h)
	paths := []string{"/", "/about", "/blog"}
	for _, b := range s.engine.GetAllBlogs() {
		paths = append(paths, "/blog/"+b.Slug)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			docs := ldJSONRe.FindAllStringSubmatch(get(t, app, path).body, -1)
			if len(docs) == 0 {
				t.Fatal("no JSON-LD found")
			}
			for _, d := range docs {
				if !json.Valid([]byte(d[1])) {
					t.Errorf("invalid JSON-LD: %s", d[1])
				}
			}
		})
	}

	var person struct {
		Type   string   `json:"@type"`
		SameAs []string `json:"sameAs"`
	}
	docs := ldJSONRe.FindAllStringSubmatch(get(t, app, "/about").body, -1)
	if len(docs) != 1 {
		t.Fatalf("about has %d JSON-LD documents", len(docs))
	}
	if err := json.Unmarshal([]byte(docs[0][1]), &person); err != nil {
		t.Fatal(err)
	}
	c := s.h.config
	if want := []string{c.GitHubURL, c.LinkedInURL, c.XURL}; person.Type != "Person" || strings.Join(person.SameAs, " ") != strings.Join(want, " ") {
		t.Errorf("Person = %+v, want sameAs %v", person, want)
	}
}

func TestJSONLDCannotBreakOutOfScript(t *testing.T) {
	config := models.DefaultConfig()
	config.Title = `</script><script>alert(1)</script>`
	config.Description = `"quoted" & <b>bold</b>`
	s := newSite(t, realContent, realProjects, realTemplates, config)
	body := get(t, newApp(s.h), "/").body

	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("JSON-LD value broke out of its script element")
	}
	docs := ldJSONRe.FindAllStringSubmatch(body, -1)
	if len(docs) == 0 {
		t.Fatal("no JSON-LD found")
	}
	var site struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(docs[0][1]), &site); err != nil {
		t.Fatal(err)
	}
	if site.Name != config.Title || site.Description != config.Description {
		t.Errorf("round trip = %+v", site)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exact", 5, "exact"},
		{"héllo wörld", 4, "héll..."},
		{"日本語テキスト", 3, "日本語..."},
		{"abc", 0, "..."},
		{"abc", -1, "..."},
	}
	for _, tt := range tests {
		if got := truncate(tt.in, tt.n); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

func TestAssetURL(t *testing.T) {
	tests := []struct {
		version, path, want string
	}{
		{"abc123", "css/style.css", "/static/css/style.css?v=abc123"},
		{"abc123", "/js/main.js", "/static/js/main.js?v=abc123"},
		{"", "img/og.png", "/static/img/og.png"},
	}
	for _, tt := range tests {
		h := &Handler{config: models.SiteConfig{AssetVersion: tt.version}}
		if got := h.assetURL(tt.path); got != tt.want {
			t.Errorf("assetURL(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestLoadTemplatesPartials(t *testing.T) {
	contentDir, _ := writeContent(t)
	base := `{{define "base"}}[{{block "main-class" .}}{{end}}]{{block "header-extra" .}}{{end}}{{template "content" .}}{{end}}{{define "base-helper"}}h{{end}}`
	tests := []struct {
		name    string
		files   map[string]string
		wantErr []string // substrings of the error; nil means LoadTemplates succeeds
	}{
		{"no partials directory", nil, nil},
		{"empty partials directory", map[string]string{"partials/.keep": ""}, nil},
		{"non-html files in partials are ignored", map[string]string{
			"partials/a.html":   `{{define "a-x"}}AX{{end}}`,
			"partials/notes.md": `{{define "a-x"}}other{{end}}`,
		}, nil},
		{"duplicate across partials", map[string]string{
			"partials/a.html": `{{define "dup"}}1{{end}}`,
			"partials/b.html": `{{define "x"}}{{end}}{{define "dup"}}2{{end}}`,
		}, []string{`"dup"`, filepath.Join("partials", "a.html"), filepath.Join("partials", "b.html")}},
		{"empty duplicate across partials", map[string]string{
			"partials/a.html": `{{define "dup"}}{{end}}`,
			"partials/b.html": `{{define "dup"}}2{{end}}`,
		}, []string{`"dup"`, filepath.Join("partials", "a.html"), filepath.Join("partials", "b.html")}},
		{"partial redefines a base hook", map[string]string{
			"partials/a.html": `{{define "header-extra"}}x{{end}}`,
		}, []string{`"header-extra"`, "base.html", filepath.Join("partials", "a.html")}},
		{"page redefines a partial", map[string]string{
			"partials/a.html": `{{define "a-x"}}AX{{end}}`,
			"home.html":       `{{define "content"}}{{template "a-x"}}{{end}}{{define "a-x"}}mine{{end}}`,
		}, []string{`"a-x"`, filepath.Join("partials", "a.html"), "home.html"}},
		{"page redefines a base template that is not a hook", map[string]string{
			"home.html": `{{define "content"}}{{end}}{{define "base-helper"}}x{{end}}`,
		}, []string{"home.html", `"base-helper"`, "header-extra, main-class"}},
		{"page without content", map[string]string{
			"about.html": `{{define "body"}}{{end}}`,
		}, []string{"about.html", `"content"`}},
		{"partial with a parse error", map[string]string{
			"partials/a.html": `{{define "a-x"}}{{if}}{{end}}`,
		}, []string{filepath.Join("partials", "a.html")}},
		{"escaping error fails at load", map[string]string{
			"contact.html": `{{define "content"}}<a title="{{.Title}}>unclosed</a>{{end}}`,
		}, []string{"template contact"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"base.html": base}
			for k, v := range tt.files {
				files[k] = v
			}
			dir := writeTemplates(t, files)
			db, err := database.New(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			engine := blog.NewEngine(db)
			if err := engine.LoadFromDir(contentDir); err != nil {
				t.Fatal(err)
			}
			h := New(engine, search.New(db), models.DefaultConfig())
			err = h.LoadTemplates(dir)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("LoadTemplates: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("LoadTemplates succeeded, want an error")
			}
			t.Logf("error: %v", err)
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// Partials are shared by every page, and pages fill base.html's hooks.
func TestTemplateHooksAndPartials(t *testing.T) {
	contentDir, projectsFile := writeContent(t)
	dir := writeTemplates(t, map[string]string{
		"base.html":       `{{define "main-class"}}{{end}}{{define "base"}}<main{{with .MainClass}} class="{{.}}"{{end}}>{{block "header-extra" .}}{{end}}|{{template "content" .}}</main>{{end}}`,
		"partials/a.html": `{{define "part-a"}}A{{template "part-b" .}}{{end}}`,
		"partials/b.html": `{{define "part-b"}}B:{{.Page}}{{end}}`,
		"home.html":       `{{define "content"}}{{template "part-a" .}}{{end}}`,
		"blog_post.html":  `{{define "header-extra"}}<div class="progress"></div>{{end}}{{define "content"}}{{template "part-b" .}}{{end}}`,
		"error.html":      `{{define "main-class"}}error{{end}}{{define "content"}}{{with dict "Code" .Code "Path" .CurrentPath}}{{.Code}} {{.Path}}{{end}}{{end}}`,
	})
	app := newApp(newSite(t, contentDir, projectsFile, dir, models.DefaultConfig()).h)
	tests := []struct {
		path string
		want string
	}{
		// <main> has a class attribute only when the page defines main-class
		{"/", `<main>|AB:home</main>`},
		{"/blog/newest", `<main><div class="progress"></div>|B:blog_post</main>`},
		{"/nope", `<main class="error">|404 /nope</main>`},
	}
	for _, tt := range tests {
		if body := get(t, app, tt.path).body; body != tt.want {
			t.Errorf("GET %s = %q, want %q", tt.path, body, tt.want)
		}
	}
}

func TestDict(t *testing.T) {
	m, err := dict("A", 1, "B", "x")
	if err != nil || len(m) != 2 || m["A"] != 1 || m["B"] != "x" {
		t.Errorf("dict = %v, %v", m, err)
	}
	if m, err := dict(); err != nil || len(m) != 0 {
		t.Errorf("empty dict = %v, %v", m, err)
	}
	if _, err := dict("A"); err == nil {
		t.Error("odd arguments: no error")
	}
	if _, err := dict(1, "x"); err == nil {
		t.Error("non-string key: no error")
	}
}

func TestKeepReading(t *testing.T) {
	post := func(slug string, tags ...string) *models.Blog { return &models.Blog{Slug: slug, Tags: tags} }
	cur := post("current", "go", "cli")
	a, b, c, d := post("a", "go"), post("b", "cli", "go"), post("c", "rust"), post("d")
	older, newer := post("older", "go"), post("newer")
	tests := []struct {
		name      string
		older     *models.Blog
		newer     *models.Blog
		suggested []*models.Blog
		want      string
	}{
		{"suggestions only", nil, nil, []*models.Blog{a, b, c, d}, "related:a,related:b,more:c,"},
		{"prev and next first", older, newer, []*models.Blog{a, b}, "prev:older,next:newer,related:a,"},
		{"adjacent posts are not repeated", older, newer, []*models.Blog{newer, older, a, b}, "prev:older,next:newer,related:a,"},
		{"only an older post", older, nil, []*models.Blog{older, a, b, c}, "prev:older,related:a,related:b,"},
		{"only a newer post", nil, newer, []*models.Blog{a, newer, b}, "next:newer,related:a,related:b,"},
		{"no shared tag is more, not related", nil, nil, []*models.Blog{c, d}, "more:c,more:d,"},
		{"too few posts", older, nil, []*models.Blog{older}, "prev:older,"},
		{"nothing at all", nil, nil, nil, ""},
		{"nil and repeated suggestions are skipped", nil, nil, []*models.Blog{nil, a, a, b}, "related:a,related:b,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got strings.Builder
			for _, it := range keepReading(cur, tt.older, tt.newer, tt.suggested) {
				got.WriteString(it.Dir + ":" + it.Post.Slug + ",")
			}
			if got.String() != tt.want {
				t.Errorf("keepReading = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

// Every real post fills all three "Keep reading" slots (the site has more
// than three posts): the adjacent posts first, never the post itself, no
// repeats.
func TestKeepReadingRealPosts(t *testing.T) {
	s := newSite(t, realContent, realProjects, writeTemplates(t, nil), models.DefaultConfig())
	app := newApp(s.h)
	keepRe := regexp.MustCompile(`;keep=([^;]*);`)
	blogs := s.engine.GetAllBlogs()
	if len(blogs) < 4 {
		t.Fatalf("want at least 4 real posts, have %d", len(blogs))
	}
	for _, b := range blogs {
		t.Run(b.Slug, func(t *testing.T) {
			m := keepRe.FindStringSubmatch(get(t, app, "/blog/"+b.Slug).body)
			if m == nil {
				t.Fatal("no keep= field in the page")
			}
			items := strings.Split(strings.TrimSuffix(m[1], ","), ",")
			if len(items) != keepReadingSlots {
				t.Fatalf("keep reading = %q, want %d items", m[1], keepReadingSlots)
			}
			newer, older := s.engine.GetAdjacentBlogs(b.Slug)
			var want []string
			if older != nil {
				want = append(want, "prev:"+older.Slug)
			}
			if newer != nil {
				want = append(want, "next:"+newer.Slug)
			}
			if got := items[:len(want)]; strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("adjacent slots = %q, want %q", got, want)
			}
			seen := map[string]bool{b.Slug: true}
			for _, it := range items {
				dir, slug, _ := strings.Cut(it, ":")
				if seen[slug] {
					t.Errorf("%s repeats or is the post itself", it)
				}
				seen[slug] = true
				other := s.engine.GetBlog(slug)
				switch dir {
				case "prev", "next":
				case "related":
					if !sharesTag(b, other) {
						t.Errorf("%s is labelled related but shares no tag with %s", slug, b.Slug)
					}
				case "more":
					if sharesTag(b, other) {
						t.Errorf("%s shares a tag with %s but is not labelled related", slug, b.Slug)
					}
				default:
					t.Errorf("unknown direction %q", dir)
				}
			}
		})
	}
}

// Front matter is typeset like the body (curly quotes, dashes, an ellipsis,
// a no-break space before a spaced dash), so every output that carries a
// post's title and description gets UTF-8 punctuation: the page, its
// JSON-LD, the RSS feed, the search index and the EPUB all stay valid and
// carry the same text. (The PDF's cp1252 is tested in internal/download.)
func TestFrontMatterTypographyOutputs(t *testing.T) {
	dir := t.TempDir()
	src := "---\ntitle: \"Go's \\\"quiet\\\" scheduler -- a tour...\"\n" +
		"description: \"Why 'cooperative' matters --- and what it costs & <why>.\"\n" +
		"date: 2026-04-01\ntags: [\"go\"]\n---\n\nBody text.\n"
	if err := os.WriteFile(filepath.Join(dir, "typeset.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSite(t, dir, realProjects, realTemplates, models.DefaultConfig())
	app := newApp(s.h)
	post := s.engine.GetBlog("typeset")
	if post == nil {
		t.Fatal("post not loaded")
	}
	const (
		wantTitle = "Go’s “quiet” scheduler\u00a0– a tour…"
		wantDesc  = "Why ‘cooperative’ matters\u00a0— and what it costs & <why>."
	)
	if post.Title != wantTitle || post.Description != wantDesc {
		t.Fatalf("front matter = %q / %q, want %q / %q", post.Title, post.Description, wantTitle, wantDesc)
	}

	// the page and its JSON-LD
	page := get(t, app, "/blog/typeset")
	if page.status != http.StatusOK {
		t.Fatalf("post page status = %d", page.status)
	}
	h1 := parseHTML(t, page.body).one(t, "h1", tagIs("h1"))
	if h1.textContent() != collapse(wantTitle) {
		t.Errorf("h1 = %q", h1.textContent())
	}
	headline := ""
	for _, d := range ldJSONRe.FindAllStringSubmatch(page.body, -1) {
		var doc struct {
			Headline    string `json:"headline"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal([]byte(d[1]), &doc); err != nil {
			t.Fatalf("invalid JSON-LD: %v\n%s", err, d[1])
		}
		if doc.Headline != "" {
			headline = doc.Headline
			if doc.Description != wantDesc {
				t.Errorf("JSON-LD description = %q", doc.Description)
			}
		}
	}
	if headline != wantTitle {
		t.Errorf("JSON-LD headline = %q, want %q", headline, wantTitle)
	}

	// the RSS feed
	var feed struct {
		Items []struct {
			Title       string `xml:"title"`
			Description string `xml:"description"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal([]byte(get(t, app, "/rss.xml").body), &feed); err != nil {
		t.Fatalf("rss.xml: %v", err)
	}
	if len(feed.Items) != 1 || feed.Items[0].Title != wantTitle || feed.Items[0].Description != wantDesc {
		t.Errorf("RSS items = %+v", feed.Items)
	}

	// the command palette's index
	var idx struct {
		Posts []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"posts"`
	}
	if err := json.Unmarshal([]byte(get(t, app, "/search-index.json").body), &idx); err != nil {
		t.Fatalf("search-index.json: %v", err)
	}
	if len(idx.Posts) != 1 || idx.Posts[0].Title != wantTitle || idx.Posts[0].Description != wantDesc {
		t.Errorf("search index posts = %+v", idx.Posts)
	}

	// the EPUB: every XML file well-formed, the title as typeset
	epub := []byte(get(t, app, "/blog/typeset/download/epub").body)
	zr, err := zip.NewReader(bytes.NewReader(epub), int64(len(epub)))
	if err != nil {
		t.Fatalf("epub: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "mimetype" || strings.HasSuffix(f.Name, ".css") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			if _, err := dec.Token(); err != nil {
				if !errors.Is(err, io.EOF) {
					t.Errorf("%s is not well-formed: %v", f.Name, err)
				}
				break
			}
		}
		if f.Name == "OEBPS/content.opf" && !bytes.Contains(data, []byte("<dc:title>Go’s “quiet” scheduler\u00a0– a tour…</dc:title>")) {
			t.Errorf("content.opf title:\n%s", data)
		}
	}

	// and the PDF is made
	pdf := get(t, app, "/blog/typeset/download/pdf")
	if pdf.status != http.StatusOK || !strings.HasPrefix(pdf.body, "%PDF-") {
		t.Errorf("PDF: status %d, %q", pdf.status, pdf.body[:min(len(pdf.body), 8)])
	}
}
