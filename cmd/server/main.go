package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/sagnikb/myspace/internal/blog"
	"github.com/sagnikb/myspace/internal/database"
	"github.com/sagnikb/myspace/internal/handlers"
	"github.com/sagnikb/myspace/internal/models"
	"github.com/sagnikb/myspace/internal/search"
)

func main() {
	// Config
	config := models.DefaultConfig()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Database
	db, err := database.New("data/portfolio.db")
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
	if err := h.LoadTemplates("templates"); err != nil {
		log.Fatalf("load templates: %v", err)
	}
	if err := h.LoadProjects("content/projects/projects.json"); err != nil {
		log.Fatalf("load projects: %v", err)
	}

	// Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).SendString("Something went wrong")
		},
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${method} ${path} ${latency}\n",
	}))
	app.Use(compress.New())
	app.Use(helmet.New())
	app.Use(limiter.New(limiter.Config{
		Max: 100,
	}))

	// Static files
	app.Static("/static", "./static", fiber.Static{
		MaxAge: 86400,
	})

	// Routes
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
	app.Get("/contact", h.Contact)
	app.Get("/rss.xml", h.RSS)
	app.Get("/sitemap.xml", h.Sitemap)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := app.Listen(":" + port); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	log.Printf("Server running on http://localhost:%s", port)
	<-quit
	log.Println("Shutting down...")
	app.Shutdown()
}
