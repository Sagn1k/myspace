package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/sagnikb/myspace/internal/blog"
	"github.com/sagnikb/myspace/internal/download"
	"github.com/sagnikb/myspace/internal/models"
	"github.com/sagnikb/myspace/internal/rss"
	"github.com/sagnikb/myspace/internal/search"
)

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

func (h *Handler) LoadTemplates(dir string) error {
	funcMap := template.FuncMap{
		"join":     strings.Join,
		"html":     func(s string) template.HTML { return template.HTML(s) },
		"truncate": truncate,
		"add":      func(a, b int) int { return a + b },
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i
			}
			return s
		},
	}

	baseFile := filepath.Join(dir, "base.html")
	pages := []string{
		"home", "about", "projects", "blog_list", "blog_post",
		"tags", "tag_posts", "search", "contact",
	}

	h.tmpls = make(map[string]*template.Template)
	for _, page := range pages {
		pageFile := filepath.Join(dir, page+".html")
		tmpl, err := template.New("").Funcs(funcMap).ParseFiles(baseFile, pageFile)
		if err != nil {
			return fmt.Errorf("parse template %s: %w", page, err)
		}
		h.tmpls[page] = tmpl
	}
	return nil
}

func (h *Handler) LoadProjects(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read projects: %w", err)
	}
	return json.Unmarshal(data, &h.projects)
}

func (h *Handler) render(c *fiber.Ctx, name string, data fiber.Map) error {
	if data == nil {
		data = fiber.Map{}
	}
	data["Config"] = h.config
	data["CurrentPath"] = c.Path()

	tmpl, ok := h.tmpls[name]
	if !ok {
		return c.Status(500).SendString("Unknown template: " + name)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", data); err != nil {
		log.Printf("template error [%s]: %v", name, err)
		return c.Status(500).SendString("Template error: " + err.Error())
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.Send(buf.Bytes())
}

// Route handlers

func (h *Handler) Home(c *fiber.Ctx) error {
	featured := h.getFeaturedProjects()
	return h.render(c, "home", fiber.Map{
		"Title":        "Home",
		"LatestBlogs":  h.engine.GetLatestBlogs(3),
		"FeaturedProjects": featured,
	})
}

func (h *Handler) About(c *fiber.Ctx) error {
	return h.render(c, "about", fiber.Map{
		"Title": "About",
	})
}

func (h *Handler) Projects(c *fiber.Ctx) error {
	return h.render(c, "projects", fiber.Map{
		"Title":    "Projects",
		"Projects": h.projects,
	})
}

func (h *Handler) BlogList(c *fiber.Ctx) error {
	return h.render(c, "blog_list", fiber.Map{
		"Title": "Blog",
		"Blogs": h.engine.GetAllBlogs(),
		"Tags":  h.engine.GetAllTags(),
	})
}

func (h *Handler) BlogPost(c *fiber.Ctx) error {
	slug := c.Params("slug")
	post := h.engine.GetBlog(slug)
	if post == nil {
		return c.Status(404).SendString("Post not found")
	}

	related := h.engine.GetRelatedBlogs(slug, 3)

	return h.render(c, "blog_post", fiber.Map{
		"Title":        post.Title,
		"Post":         post,
		"RelatedPosts": related,
	})
}

func (h *Handler) Tags(c *fiber.Ctx) error {
	return h.render(c, "tags", fiber.Map{
		"Title": "Tags",
		"Tags":  h.engine.GetAllTags(),
	})
}

func (h *Handler) TagPosts(c *fiber.Ctx) error {
	tag := c.Params("tag")
	blogs := h.engine.GetBlogsByTag(tag)

	return h.render(c, "tag_posts", fiber.Map{
		"Title": fmt.Sprintf("Posts tagged: %s", tag),
		"Tag":   tag,
		"Blogs": blogs,
	})
}

func (h *Handler) Search(c *fiber.Ctx) error {
	query := c.Query("q")
	var results []models.SearchResult

	if query != "" {
		var err error
		results, err = h.search.Search(query)
		if err != nil {
			results = nil
		}
	}

	return h.render(c, "search", fiber.Map{
		"Title":   "Search",
		"Query":   query,
		"Results": results,
	})
}

func (h *Handler) Contact(c *fiber.Ctx) error {
	return h.render(c, "contact", fiber.Map{
		"Title": "Contact",
	})
}

func (h *Handler) RSS(c *fiber.Ctx) error {
	blogs := h.engine.GetAllBlogs()
	data, err := rss.Generate(blogs, h.config)
	if err != nil {
		return c.Status(500).SendString("Error generating RSS")
	}
	c.Set("Content-Type", "application/rss+xml; charset=utf-8")
	return c.Send(data)
}

func (h *Handler) Sitemap(c *fiber.Ctx) error {
	blogs := h.engine.GetAllBlogs()
	tags := h.engine.GetAllTags()

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)

	pages := []string{"/", "/about", "/projects", "/blog", "/tags", "/contact"}
	for _, p := range pages {
		fmt.Fprintf(&b, `<url><loc>%s%s</loc></url>`, h.config.BaseURL, p)
	}
	for _, blog := range blogs {
		fmt.Fprintf(&b, `<url><loc>%s/blog/%s</loc><lastmod>%s</lastmod></url>`,
			h.config.BaseURL, blog.Slug, blog.Date.Format("2006-01-02"))
	}
	for _, tag := range tags {
		fmt.Fprintf(&b, `<url><loc>%s/tags/%s</loc></url>`, h.config.BaseURL, tag.Name)
	}

	b.WriteString(`</urlset>`)
	c.Set("Content-Type", "application/xml; charset=utf-8")
	return c.SendString(b.String())
}

func (h *Handler) DownloadPDF(c *fiber.Ctx) error {
	slug := c.Params("slug")
	post := h.engine.GetBlog(slug)
	if post == nil {
		return c.Status(404).SendString("Post not found")
	}

	data, err := download.GeneratePDF(post)
	if err != nil {
		return c.Status(500).SendString("Error generating PDF")
	}

	c.Set("Content-Type", "application/pdf")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.pdf"`, slug))
	return c.Send(data)
}

func (h *Handler) DownloadEPUB(c *fiber.Ctx) error {
	slug := c.Params("slug")
	post := h.engine.GetBlog(slug)
	if post == nil {
		return c.Status(404).SendString("Post not found")
	}

	data, err := download.GenerateEPUB(post)
	if err != nil {
		return c.Status(500).SendString("Error generating EPUB")
	}

	c.Set("Content-Type", "application/epub+zip")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.epub"`, slug))
	return c.Send(data)
}

func (h *Handler) getFeaturedProjects() []models.Project {
	var featured []models.Project
	for _, p := range h.projects {
		if p.Featured {
			featured = append(featured, p)
		}
	}
	return featured
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
