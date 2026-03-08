package blog

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/sagnikb/myspace/internal/models"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var md goldmark.Markdown

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			meta.Meta,
			highlighting.NewHighlighting(
				highlighting.WithStyle("dracula"),
				highlighting.WithFormatOptions(),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)
}

func Parse(source []byte) (*models.Blog, error) {
	var buf bytes.Buffer
	ctx := parser.NewContext()

	if err := md.Convert(source, &buf, parser.WithContext(ctx)); err != nil {
		return nil, fmt.Errorf("markdown convert: %w", err)
	}

	metadata := meta.Get(ctx)

	blog := &models.Blog{
		HTMLContent: buf.String(),
		Content:     extractPlainText(source),
		Status:      "published",
	}

	if v, ok := metadata["title"].(string); ok {
		blog.Title = v
	}
	if v, ok := metadata["description"].(string); ok {
		blog.Description = v
	}
	if v, ok := metadata["status"].(string); ok {
		blog.Status = v
	}

	switch v := metadata["date"].(type) {
	case string:
		if t, err := time.Parse("2006-01-02", v); err == nil {
			blog.Date = t
		}
	case time.Time:
		blog.Date = v
	}

	switch v := metadata["tags"].(type) {
	case []interface{}:
		for _, item := range v {
			if tag, ok := item.(string); ok {
				tag = strings.TrimSpace(tag)
				if tag != "" {
					blog.Tags = append(blog.Tags, tag)
				}
			}
		}
	case string:
		for _, tag := range strings.Split(v, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				blog.Tags = append(blog.Tags, tag)
			}
		}
	}

	blog.ReadingTime = estimateReadingTime(blog.Content)
	blog.TOC = extractTOC(source)

	return blog, nil
}

func estimateReadingTime(text string) int {
	words := len(strings.Fields(text))
	minutes := words / 200
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}

var headingRe = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)

func extractTOC(source []byte) []models.TOCItem {
	matches := headingRe.FindAllSubmatch(source, -1)
	var items []models.TOCItem
	for _, m := range matches {
		level := len(m[1])
		title := string(m[2])
		id := slugify(title)
		items = append(items, models.TOCItem{
			Level: level,
			ID:    id,
			Title: title,
		})
	}
	return items
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func extractPlainText(source []byte) string {
	lines := strings.Split(string(source), "\n")
	var text []string
	inFrontmatter := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			inFrontmatter = !inFrontmatter
			continue
		}
		if !inFrontmatter {
			text = append(text, line)
		}
	}
	return strings.Join(text, "\n")
}
