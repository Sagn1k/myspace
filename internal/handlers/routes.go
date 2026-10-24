package handlers

import (
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Register mounts the static files, every route and, last, the catch-all 404.
// The app should use ErrorHandler as its fiber.Config.ErrorHandler.
func (h *Handler) Register(app *fiber.App, staticDir string) {
	app.Use(canonicalPath)
	app.Static("/static", staticDir, fiber.Static{
		MaxAge: 86400,
	})
	// The WorkWatch installer and release tarballs keep their published URLs
	// (curl -fsSL https://sagnikbhowmick.com/workwatch/install.sh | bash).
	// Without a static/workwatch directory, requests fall through to the 404.
	app.Static("/workwatch", filepath.Join(staticDir, "workwatch"), fiber.Static{
		MaxAge: 3600,
	})

	app.Get("/", h.Home)
	app.Get("/about", h.About)
	app.Get("/projects", h.Projects)
	app.Get("/blog", h.BlogList)
	app.Get("/blog/:slug", h.BlogPost)
	app.Get("/blog/:slug/download/pdf", h.DownloadPDF)
	app.Get("/blog/:slug/download/epub", h.DownloadEPUB)
	app.Get("/tags", h.Tags)
	app.Get("/tags/:tag", h.TagPosts)
	app.Get("/search", h.Search)
	app.Get("/search-index.json", h.SearchIndex)
	app.Get("/contact", h.Contact)
	app.Get("/rss.xml", h.RSS)
	app.Get("/sitemap.xml", h.Sitemap)
	app.Get("/robots.txt", h.Robots)
	app.Get("/favicon.ico", h.Favicon)

	app.Use(h.NotFound)
}

// canonicalPath answers a GET or HEAD for a page path that ends in "/"
// ("/blog/", "/tags/go//") with a 301 to the same path without the trailing
// slashes, keeping the query string. Fiber would otherwise serve the page at
// both URLs, and the canonical link, og:url and the nav's aria-current are
// computed from the requested path. "/" itself and the static trees
// (/static/, /workwatch/) are left alone.
//
// The Location is always a same-site path: leading slashes collapse to one
// ("//evil.com/" goes to "/evil.com", never to another host), and a path
// holding anything but plain URL path characters (a backslash, a space, a
// control character) is not redirected at all.
func canonicalPath(c *fiber.Ctx) error {
	if m := c.Method(); m != fiber.MethodGet && m != fiber.MethodHead {
		return c.Next()
	}
	p := c.Path()
	if len(p) < 2 || p[len(p)-1] != '/' || !plainPath(p) ||
		strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/workwatch/") {
		return c.Next()
	}
	target := "/" + strings.TrimLeft(strings.TrimRight(p, "/"), "/")
	if q := c.Request().URI().QueryString(); len(q) > 0 {
		target += "?" + string(q)
	}
	return c.Redirect(target, fiber.StatusMovedPermanently)
}

// plainPath reports whether p is made only of RFC 3986 path characters:
// letters, digits, "-._~", the sub-delims, ":", "@", "/" and "%" escapes.
func plainPath(p string) bool {
	for i := 0; i < len(p); i++ {
		b := p[i]
		if 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' {
			continue
		}
		if !strings.ContainsRune("-._~!$&'()*+,;=:@/%", rune(b)) {
			return false
		}
	}
	return true
}
