# myspace

A developer portfolio and knowledge blog built with Go. Features a clean, Linear-inspired dark theme, markdown-based blog engine, full-text search, and blog post downloads in PDF/EPUB formats.

**Live:** [sagnikbhowmick.com](https://sagnikbhowmick.com)

## Features

- Markdown blog engine with frontmatter, syntax highlighting, and auto-generated TOC
- Full-text search powered by SQLite FTS5
- Blog post download as PDF and EPUB
- RSS feed and XML sitemap
- Tag-based navigation
- Dark/light theme toggle
- Reading progress bar
- Related posts by shared tags
- Rate limiting, compression, and security headers
- Graceful shutdown

## Tech Stack

- **Backend:** Go, [Fiber](https://gofiber.io/) v2
- **Markdown:** [Goldmark](https://github.com/yuin/goldmark) with GFM, frontmatter (goldmark-meta), and syntax highlighting (Chroma)
- **Database:** SQLite via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO)
- **PDF:** [go-pdf/fpdf](https://github.com/go-pdf/fpdf)
- **Frontend:** TailwindCSS, vanilla JS
- **Templating:** Go `html/template`

## Project Structure

```
cmd/server/          Entry point
internal/
  blog/              Markdown parser and blog engine
  database/          SQLite persistence and FTS5 indexing
  download/          PDF and EPUB generators
  handlers/          HTTP route handlers
  models/            Data models and config
  rss/               RSS feed generator
  search/            Search engine wrapper
content/
  blogs/             Markdown blog posts
  projects/          projects.json
templates/           Go HTML templates
static/
  css/               Custom styles
  js/                Theme toggle, reading progress, code copy
```

## Getting Started

### Prerequisites

- Go 1.24+

### Run

```bash
go build -o portfolio ./cmd/server/
./portfolio
```

The server starts on `http://localhost:8080`. Set the `PORT` environment variable to change it:

```bash
PORT=3000 ./portfolio
```

### Adding Blog Posts

Create a markdown file in `content/blogs/` with frontmatter:

```markdown
---
title: "Your Post Title"
slug: "your-post-title"
date: 2026-01-15
description: "A short description for previews and SEO."
tags: ["go", "backend"]
status: "published"
---

Your markdown content here.
```

Restart the server to pick up new posts.

### Adding Projects

Edit `content/projects/projects.json`:

```json
[
  {
    "name": "Project Name",
    "description": "What it does.",
    "tags": ["go", "cli"],
    "github": "https://github.com/you/project",
    "demo": "",
    "featured": true
  }
]
```

## Routes

| Path | Description |
|------|-------------|
| `/` | Home page |
| `/about` | About page |
| `/projects` | Projects listing |
| `/blog` | Blog listing |
| `/blog/:slug` | Blog post |
| `/blog/:slug/download/pdf` | Download post as PDF |
| `/blog/:slug/download/epub` | Download post as EPUB |
| `/tags` | Tag cloud |
| `/tags/:tag` | Posts by tag |
| `/search?q=term` | Full-text search |
| `/contact` | Contact page |
| `/rss.xml` | RSS feed |
| `/sitemap.xml` | Sitemap |

## License

MIT
