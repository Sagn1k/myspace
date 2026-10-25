package main

import (
	"log"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/sagnikb/myspace/internal/assets"
	"github.com/sagnikb/myspace/internal/blog"
	"github.com/sagnikb/myspace/internal/database"
	"github.com/sagnikb/myspace/internal/handlers"
	"github.com/sagnikb/myspace/internal/models"
	"github.com/sagnikb/myspace/internal/search"
)

// revision is the git commit the binary was built from, exposed as
// <meta name="revision"> in every page's head. Set it at build time:
//
//	go build -ldflags "-X main.revision=$(git rev-parse --short HEAD)" ./cmd/server
//
// The REVISION environment variable, when set, overrides it (CI passes the
// full SHA through the Cloud Run environment).
var revision = "dev"

func main() {
	// Config
	config := models.DefaultConfig()
	config.Revision = resolveRevision(revision, os.Getenv("REVISION"))
	port := getenv("PORT", "8080")
	dbPath := getenv("DB_PATH", "data/portfolio.db")

	version, err := assets.Version("static")
	if err != nil {
		log.Fatalf("asset version: %v", err)
	}
	config.AssetVersion = version
	config.Favicon, config.FaviconType = siteIcon("static", config.Favicon, config.FaviconType)

	// Database
	db, err := database.New(dbPath)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	// Blog engine
	engine := blog.NewEngine(db)
	if err := engine.LoadFromDir("content/blogs"); err != nil {
		log.Fatalf("load blogs: %v", err)
	}
	log.Printf("Loaded %d blog posts", len(engine.GetAllBlogs()))

	// Search engine
	searchEngine := search.New(db)

	// Handlers
	h := handlers.New(engine, searchEngine, config)
	if err := h.LoadTemplates(getenv("TEMPLATES_DIR", "templates")); err != nil {
		log.Fatalf("load templates: %v", err)
	}
	if err := h.LoadProjects("content/projects/projects.json"); err != nil {
		log.Fatalf("load projects: %v", err)
	}

	// Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: h.ErrorHandler,
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${method} ${path} ${latency}\n",
	}))
	app.Use(compression())
	app.Use(helmet.New(helmet.Config{
		CrossOriginEmbedderPolicy: "unsafe-none",
	}))
	// Rate limiting handled by Cloud Run concurrency settings

	// Static files, routes and the catch-all 404
	h.Register(app, "./static")

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := app.Listen(":" + port); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	log.Printf("Server running on http://localhost:%s (assets v=%s, revision %s)", port, config.AssetVersion, config.Revision)
	<-quit
	log.Println("Shutting down...")
	app.Shutdown()
}

// compression compresses responses, except those that are compressed
// already (precompressed).
func compression() fiber.Handler {
	return compress.New(compress.Config{Next: precompressed})
}

// precompressed reports whether the request is for a file that is
// compressed already: an archive (the WorkWatch release tarball), an image,
// a font, or a post's PDF or EPUB download. gzip or brotli makes such a body
// no smaller, but drops its Content-Length (no download size or progress)
// and holds the whole body in memory while encoding it.
func precompressed(c *fiber.Ctx) bool {
	p := c.Path()
	if strings.HasPrefix(p, "/blog/") && strings.Contains(p, "/download/") {
		return true
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".gz", ".tgz", ".zip", ".br", ".zst", ".xz", ".bz2",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif",
		".woff", ".woff2", ".pdf", ".epub", ".mp4", ".webm":
		return true
	}
	return false
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// resolveRevision picks the revision to display: env when set, else the build
// value, else "dev". A hex commit SHA longer than seven characters is
// shortened to its first seven.
func resolveRevision(build, env string) string {
	r := strings.TrimSpace(env)
	if r == "" {
		r = strings.TrimSpace(build)
	}
	if r == "" {
		return "dev"
	}
	if len(r) > 7 && isHex(r) {
		r = r[:7]
	}
	return r
}

// siteIcon returns the site icon's path under staticDir and its MIME type:
// the owner's favicon.ico when staticDir has one, otherwise the given default.
func siteIcon(staticDir, path, mimeType string) (string, string) {
	if fi, err := os.Stat(filepath.Join(staticDir, "favicon.ico")); err == nil && fi.Mode().IsRegular() {
		return "favicon.ico", "image/x-icon"
	}
	return path, mimeType
}

func isHex(s string) bool {
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return s != ""
}
