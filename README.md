# myspace

A developer portfolio and knowledge blog built with Go. An editorial design (Newsreader and JetBrains Mono on warm paper, one vermilion accent, light and dark), a markdown blog engine, full-text search, and posts downloadable as PDF and EPUB.

**Live:** [sagnikbhowmick.com](https://sagnikbhowmick.com)

## Features

- Markdown blog engine with frontmatter, typographic punctuation, syntax highlighting, and a table of contents
- Full-text search powered by SQLite FTS5, plus a ⌘K / Ctrl K command palette
- Posts downloadable as PDF (typeset from the markdown) and EPUB 3
- RSS feed, XML sitemap and JSON-LD structured data
- Tag pages and a topic index
- Light and dark themes, following the OS until you pick one
- "Keep reading" suggestions by shared tags
- Compression and security headers (rate limiting is left to Cloud Run)
- Graceful shutdown

## Tech Stack

- **Backend:** Go, [Fiber](https://gofiber.io/) v2
- **Markdown:** [Goldmark](https://github.com/yuin/goldmark) with GFM, frontmatter (goldmark-meta), and syntax highlighting (Chroma)
- **Database:** SQLite via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO)
- **PDF:** [go-pdf/fpdf](https://github.com/go-pdf/fpdf)
- **Frontend:** one hand-written stylesheet and one vanilla JS file, no framework or build step; Newsreader and JetBrains Mono from Google Fonts
- **Templating:** Go `html/template`

## Project Structure

```
cmd/server/          Entry point
internal/
  assets/            Static asset version hash (cache busting)
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
  js/                Theme, command palette, heading self-links, copy buttons
  img/               Favicon and social image
  workwatch/         WorkWatch installer and release tarballs (/workwatch/...)
```

## Getting Started

### Prerequisites

- Go 1.26+

### Run

```bash
go build -o portfolio ./cmd/server/
./portfolio
```

The server starts on `http://localhost:8080`. Set the `PORT` environment variable to change it:

```bash
PORT=3000 ./portfolio
```

The SQLite search database defaults to `data/portfolio.db`; set `DB_PATH` to put it elsewhere.

### Test

```bash
go test ./...
```

### Adding Blog Posts

Create a markdown file in `content/blogs/` with frontmatter. The file name is the slug (`your-post-title.md` is served at `/blog/your-post-title`):

```markdown
---
title: "Your Post Title"
date: 2026-01-15
description: "A short description for previews and SEO."
tags: ["go", "backend"]
pullquote: "Optional: a standout sentence from the post (not shown at present)."
status: "published"
---

Your markdown content here.
```

`status` defaults to `published`; any other value keeps the post off the site. Restart the server to pick up new posts.

### Adding Projects

Edit `content/projects/projects.json`:

```json
[
  {
    "id": "project-name",
    "title": "Project Name",
    "description": "What it does, in a sentence or two.",
    "summary": "Optional one-line summary for compact lists",
    "tech_stack": ["Go", "SQLite"],
    "github_link": "https://github.com/you/project",
    "demo_link": "",
    "featured": true,
    "install": "Optional one-line install command",
    "figure": ""
  }
]
```

`id` is the project's anchor (`/projects#project-name`). Without a `summary`, compact lists use the description's first sentence. `figure` is currently unused: no page draws a project figure, so leave it empty.

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
| `/tags` | Topic index |
| `/tags/:tag` | Posts by tag |
| `/search?q=term` | Full-text search |
| `/contact` | Contact page |
| `/search-index.json` | Posts and projects index for the command palette |
| `/rss.xml` | RSS feed |
| `/sitemap.xml` | Sitemap |
| `/robots.txt` | Robots rules |
| `/favicon.ico` | Redirects to `/static/favicon.ico` (or `/static/img/favicon.svg` when there is no `.ico`) |
| `/workwatch/*` | WorkWatch installer and releases (`static/workwatch/`) |

## License

MIT
