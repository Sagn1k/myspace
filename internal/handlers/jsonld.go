package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/sagnikb/myspace/internal/models"
)

const blogDescription = "Articles on software development, Go, distributed systems, and developer tools."

// jsonValue encodes v as JSON for embedding in a JSON-LD document.
// encoding/json escapes <, > and &, so "</script>" in a value cannot end the
// surrounding script element.
func jsonValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// ldJSON wraps JSON-LD documents in script tags. Documents must be built from
// constant JSON plus jsonValue-encoded values.
func ldJSON(docs ...string) template.HTML {
	var b strings.Builder
	for _, doc := range docs {
		b.WriteString(`<script type="application/ld+json">`)
		b.WriteString(doc)
		b.WriteString(`</script>`)
	}
	return template.HTML(b.String())
}

func (h *Handler) websiteSchema() string {
	return fmt.Sprintf(`{"@context":"https://schema.org","@type":"WebSite","name":%s,"url":%s,"description":%s,"author":{"@type":"Person","name":%s},"potentialAction":{"@type":"SearchAction","target":{"@type":"EntryPoint","urlTemplate":%s},"query-input":"required name=search_term_string"}}`,
		jsonValue(h.config.Title), jsonValue(h.config.BaseURL), jsonValue(h.config.Description),
		jsonValue(h.config.Author), jsonValue(h.config.BaseURL+"/search?q={search_term_string}"))
}

func (h *Handler) personSchema() string {
	sameAs := []string{h.config.GitHubURL, h.config.LinkedInURL, h.config.XURL}
	return fmt.Sprintf(`{"@context":"https://schema.org","@type":"Person","name":%s,"url":%s,"jobTitle":%s,"description":"Backend engineer building distributed systems and developer tools in Go.","sameAs":%s}`,
		jsonValue(h.config.Author), jsonValue(h.config.BaseURL), jsonValue(h.config.Role), jsonValue(sameAs))
}

func (h *Handler) blogSchema(blogs []*models.Blog) string {
	posts := make([]string, 0, len(blogs))
	for _, b := range blogs {
		posts = append(posts, fmt.Sprintf(`{"@type":"BlogPosting","headline":%s,"url":%s,"datePublished":%s,"description":%s}`,
			jsonValue(b.Title), jsonValue(h.postURL(b)), jsonValue(b.Date.Format("2006-01-02")), jsonValue(b.Description)))
	}
	return fmt.Sprintf(`{"@context":"https://schema.org","@type":"Blog","name":%s,"url":%s,"description":%s,"author":{"@type":"Person","name":%s},"blogPost":[%s]}`,
		jsonValue(h.config.Title+" Blog"), jsonValue(h.config.BaseURL+"/blog"), jsonValue(blogDescription),
		jsonValue(h.config.Author), strings.Join(posts, ","))
}

func (h *Handler) postingSchema(post *models.Blog) string {
	keywords := post.Tags
	if keywords == nil {
		keywords = []string{}
	}
	return fmt.Sprintf(`{"@context":"https://schema.org","@type":"BlogPosting","headline":%s,"description":%s,"datePublished":%s,"author":{"@type":"Person","name":%s,"url":%s},"publisher":{"@type":"Person","name":%s},"mainEntityOfPage":{"@type":"WebPage","@id":%s},"keywords":%s,"wordCount":%d,"timeRequired":"PT%dM"}`,
		jsonValue(post.Title), jsonValue(post.Description), jsonValue(post.Date.Format("2006-01-02T15:04:05Z07:00")),
		jsonValue(h.config.Author), jsonValue(h.config.BaseURL), jsonValue(h.config.Author),
		jsonValue(h.postURL(post)), jsonValue(keywords),
		len(strings.Fields(post.Content)), post.ReadingTime)
}

func (h *Handler) breadcrumbSchema(post *models.Blog) string {
	return fmt.Sprintf(`{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[{"@type":"ListItem","position":1,"name":"Home","item":%s},{"@type":"ListItem","position":2,"name":"Blog","item":%s},{"@type":"ListItem","position":3,"name":%s,"item":%s}]}`,
		jsonValue(h.config.BaseURL), jsonValue(h.config.BaseURL+"/blog"), jsonValue(post.Title), jsonValue(h.postURL(post)))
}

func (h *Handler) postURL(post *models.Blog) string {
	return h.config.BaseURL + "/blog/" + post.Slug
}
