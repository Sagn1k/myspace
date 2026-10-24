package models

import (
	"html/template"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Blog struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	// Pullquote is an optional line from the post (frontmatter "pullquote")
	// that the home page can feature.
	Pullquote string `json:"-"`
	// Content is the raw markdown body (frontmatter removed).
	Content string `json:"-"`
	// PlainText is the prose without markdown syntax (headings, paragraphs,
	// list items, quotes, table cells; no code blocks), one block per line.
	// Search indexes it and shows snippets from it.
	PlainText string `json:"-"`
	// CodeText is the text of the code blocks, one code line per line. Search
	// indexes it so identifiers stay findable, but never quotes it.
	CodeText    string    `json:"-"`
	HTMLContent string    `json:"-"`
	Date        time.Time `json:"date"`
	ReadingTime int       `json:"reading_time"`
	Tags        []string  `json:"tags"`
	Status      string    `json:"status"`
	TOC         []TOCItem `json:"toc"`
}

type TOCItem struct {
	Level int    `json:"level"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Project struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Summary is an optional one-line description for compact lists; see
	// ShortSummary.
	Summary    string   `json:"summary,omitempty"`
	TechStack  []string `json:"tech_stack"`
	GithubLink string   `json:"github_link"`
	DemoLink   string   `json:"demo_link"`
	Featured   bool     `json:"featured"`
	// Figure names an optional drawing for the project (e.g. "workwatch"
	// renders the "fig-workwatch" partial). Empty, or a name no partial
	// draws, means no figure.
	Figure string `json:"figure,omitempty"`
	// Install is an optional one-line install command, shown as is.
	Install string `json:"install,omitempty"`
}

// ShortSummary is Summary when set, otherwise the first sentence of Blurb
// without its trailing period.
func (p Project) ShortSummary() string {
	if s := strings.TrimSpace(p.Summary); s != "" {
		return s
	}
	return firstSentence(p.Blurb())
}

// Blurb is Description as the pages show it, under the project's title: a
// description copied from a repository often repeats the name first
// ("pixelQR: QR code generation"), so a leading "<Title>:" or "<Title> —"
// is dropped and the rest starts with a capital.
func (p Project) Blurb() string {
	d := strings.TrimSpace(p.Description)
	title := strings.TrimSpace(p.Title)
	if title == "" || len(d) <= len(title) || !strings.EqualFold(d[:len(title)], title) {
		return d
	}
	rest := strings.TrimLeft(d[len(title):], " ")
	for _, sep := range []string{":", "—", "–", "- "} {
		if r, ok := strings.CutPrefix(rest, sep); ok {
			if r = strings.TrimSpace(r); r != "" {
				first, size := utf8.DecodeRuneInString(r)
				return string(unicode.ToUpper(first)) + r[size:]
			}
		}
	}
	return d
}

// firstSentence returns s up to the first '.', '!' or '?' that ends the text
// or is followed by whitespace, with a final '.' removed. Whitespace runs
// (line breaks included) are collapsed to single spaces.
func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	end := len(s)
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c == '.' || c == '!' || c == '?') && (i+1 == len(s) || s[i+1] == ' ') {
			end = i + 1
			break
		}
	}
	return strings.TrimSuffix(s[:end], ".")
}

type TagInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type YearGroup struct {
	Year  int     `json:"year"`
	Posts []*Blog `json:"posts"`
}

// KeepItem is one slot of a post's "Keep reading" list.
type KeepItem struct {
	// Dir is "prev" (the next older post), "next" (the next newer post),
	// "related" (a suggestion that shares a tag with the post; most shared
	// tags first, then newest first) or "more" (a suggestion that shares
	// none: the newest posts).
	Dir  string
	Post *Blog
}

// SearchResult is a full-text search row. Title and Snippet contain the
// database highlight markers around matched terms.
type SearchResult struct {
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

// SearchHit is a search result ready for templates. TitleHTML and Snippet are
// HTML-escaped text with matches wrapped in <mark></mark>.
type SearchHit struct {
	Post      *Blog
	TitleHTML template.HTML
	Snippet   template.HTML
}

type SiteConfig struct {
	Title           string
	Description     string
	Author          string
	Domain          string
	BaseURL         string
	Role            string
	YearsExperience string
	Email           string
	GitHubURL       string
	GitHubHandle    string
	LinkedInURL     string
	LinkedInHandle  string
	XURL            string
	XHandle         string
	// AssetVersion is a short hash of the static files, appended to asset URLs.
	AssetVersion string
	// Revision is the deployed git commit (7-character short SHA) or "dev";
	// main sets it from the build or the REVISION environment variable.
	Revision string
	// Favicon is the site icon's path under static/ and FaviconType its MIME
	// type. main switches them to the owner's favicon.ico when static/ has one.
	Favicon     string
	FaviconType string
}

func DefaultConfig() SiteConfig {
	return SiteConfig{
		Title:           "Sagnik Bhowmick",
		Description:     "Sagnik Bhowmick is a software engineer with 7+ years of experience building backend systems, distributed architectures, and developer tools.",
		Author:          "Sagnik Bhowmick",
		Domain:          "sagnikbhowmick.com",
		BaseURL:         "https://sagnikbhowmick.com",
		Role:            "Software Engineer",
		YearsExperience: "7+",
		Email:           "hello@sagnikbhowmick.com",
		GitHubURL:       "https://github.com/Sagn1k",
		GitHubHandle:    "Sagn1k",
		LinkedInURL:     "https://www.linkedin.com/in/hellosagnik/",
		LinkedInHandle:  "hellosagnik",
		XURL:            "https://x.com/thefarzibatman",
		XHandle:         "thefarzibatman",
		Favicon:         "img/favicon.svg",
		FaviconType:     "image/svg+xml",
	}
}
