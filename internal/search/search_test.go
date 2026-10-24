package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnikb/myspace/internal/blog"
	"github.com/sagnikb/myspace/internal/database"
	"github.com/sagnikb/myspace/internal/models"
)

// q is the expression buildQuery makes from these (lower-case) terms.
func q(terms ...string) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		if len([]rune(t)) < minPrefixRunes {
			parts[i] = `"` + t + `"`
		} else {
			parts[i] = `("` + t + `" OR "` + t + `"*)`
		}
	}
	return strings.Join(parts, " AND ")
}

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"whitespace", "   \t\n", ""},
		{"plain words", "container networking", `("container" OR "container"*) AND ("networking" OR "networking"*)`},
		{"short terms are whole words", "go is ok", `"go" AND "is" AND "ok"`},
		{"three characters is a prefix", "ipt", `("ipt" OR "ipt"*)`},
		{"c++", "c++", `"c"`},
		{"lone quote", `"`, ""},
		{"quoted phrase", `"docker bridge"`, q("docker", "bridge")},
		{"unbalanced quote", `go "rust`, q("go", "rust")},
		{"colon column filter", "title:go", q("title", "go")},
		{"hyphen", "e-mail", q("e", "mail")},
		{"leading minus", "-go", q("go")},
		{"operators are terms", "go AND rust OR NOT NEAR(x)", q("go", "and", "rust", "or", "not", "near", "x")},
		{"star and caret", "^foo* bar*", q("foo", "bar")},
		{"parens and braces", "({title}: x)", q("title", "x")},
		{"punctuation only", `+-*/^:()"'{}[]<>.,;!?`, ""},
		{"case-insensitive dedupe", "Go go GO", q("go")},
		{"digits", "port 8080", q("port", "8080")},
		{"underscore splits", "pg_stat_statements", q("pg", "stat", "statements")},
		{"accents", "Café naïve", q("café", "naïve")},
		{"combining mark stays in token", "naïve", q("naïve")},
		{"lone combining mark", "́", ""},
		{"cjk", "日本語 検索", q("日本語", "検索")},
		{"emoji dropped", "🚀 launch", q("launch")},
		{"html", "<script>alert(1)</script>", q("script", "alert", "1")},
		{"sql-ish", "'; DROP TABLE blogs_fts; --", q("drop", "table", "blogs", "fts")},
		{"nul byte", "a\x00b", q("a", "b")},
		{"token cap", "a b c d e f g h i j", q("a", "b", "c", "d", "e", "f", "g", "h")},
		{"long token is truncated", strings.Repeat("a", 500), q(strings.Repeat("a", maxInputRunes))},
		{"input cap drops later words", strings.Repeat(" ", maxInputRunes) + "go", ""},
		{"input cap counts runes", strings.Repeat("é", maxInputRunes+5), q(strings.Repeat("é", maxInputRunes))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildQuery(tt.input); got != tt.want {
				t.Errorf("buildQuery(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHighlightHTML(t *testing.T) {
	const (
		s = database.MarkStart
		e = database.MarkEnd
	)
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain", "hello world", "hello world"},
		{"marked", "a " + s + "b" + e + " c", "a <mark>b</mark> c"},
		{"script is escaped", `<script>alert("x")</script>`, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;"},
		{"marked script is escaped", s + "<script>" + e + "x</script>", "<mark>&lt;script&gt;</mark>x&lt;/script&gt;"},
		{"attribute breakout", `"><img src=x onerror=alert(1)>`, "&#34;&gt;&lt;img src=x onerror=alert(1)&gt;"},
		{"ampersand", "AT&T " + s + "&" + e, "AT&amp;T <mark>&amp;</mark>"},
		{"stray end marker", "a" + e + "b", "ab"},
		{"unclosed start marker", s + "a", "<mark>a</mark>"},
		{"repeated start marker", s + "a" + s + "b" + e, "<mark>ab</mark>"},
		{"multiple marks", s + "a" + e + " " + s + "b" + e, "<mark>a</mark> <mark>b</mark>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(highlightHTML(tt.input)); got != tt.want {
				t.Errorf("highlightHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func newTestSearch(t *testing.T, blogs ...models.Blog) *Engine {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, b := range blogs {
		if err := db.IndexBlog(b); err != nil {
			t.Fatal(err)
		}
	}
	return New(db)
}

var testBlogs = []models.Blog{
	{
		Slug:        "networking",
		Title:       "Understanding Container Networking",
		Description: "Namespaces, veth pairs and bridges.",
		Tags:        []string{"docker", "linux"},
		PlainText:   "Docker uses iptables for NAT. Never paste <script>alert(1)</script> into a shell.",
		CodeText:    "sudo iptables -t nat -A POSTROUTING -j MASQUERADE",
	},
	{
		Slug:        "postgres",
		Title:       "PostgreSQL Performance Tuning",
		Description: "Indexes and EXPLAIN plans.",
		Tags:        []string{"databases"},
		PlainText:   "Composite indexes make queries fast. Kubernetes is mentioned once here.",
	},
	{
		Slug:        "kubernetes",
		Title:       "Kubernetes in Practice",
		Description: "Running containers at scale.",
		Tags:        []string{"devops"},
		PlainText:   "Pods, services and deployments.",
	},
}

func TestSearchSpecialInputs(t *testing.T) {
	e := newTestSearch(t, testBlogs...)
	inputs := []string{
		"c++", `"`, `""`, "'", "(", ")", "*", ":", "-", "^", "{", "}",
		"AND", "OR", "NOT", "NEAR", "a OR", `"unterminated`, "foo:bar", "{title}:x",
		"title:docker", "NEAR(docker linux, 2)", "日本語", strings.Repeat("x", 1000),
		strings.Repeat("go ", 200), "\x00", "%", `\`, "docker*", "-docker", "🚀",
	}
	for _, in := range inputs {
		if _, err := e.Search(in); err != nil {
			t.Errorf("Search(%q) error: %v", in, err)
		}
	}
}

func TestSearchResults(t *testing.T) {
	e := newTestSearch(t, testBlogs...)
	tests := []struct {
		name  string
		input string
		want  []string // slugs in rank order
	}{
		{"no terms", "++", nil},
		{"title beats content", "kubernetes", []string{"kubernetes", "postgres"}},
		{"prefix match", "ipt", []string{"networking"}},
		{"implicit and", "docker nat", []string{"networking"}},
		{"implicit and without common doc", "docker composite", nil},
		{"tags are searchable", "linux", []string{"networking"}},
		{"special characters around a term", `"docker"!!`, []string{"networking"}},
		{"code is searchable", "masquerade", []string{"networking"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := e.Search(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range results {
				got = append(got, r.Slug)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("Search(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSearchHighlightsAreSafe(t *testing.T) {
	e := newTestSearch(t, testBlogs...)
	results, err := e.Search("iptables alert")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	snippet := string(results[0].Snippet)
	if !strings.Contains(snippet, "<mark>iptables</mark>") {
		t.Errorf("snippet missing highlighted term: %s", snippet)
	}
	if strings.Contains(snippet, "<script>") || !strings.Contains(snippet, "&lt;script&gt;<mark>alert</mark>") {
		t.Errorf("snippet not escaped: %s", snippet)
	}

	// "container"* also matches "containers" in another post's description;
	// the title match must rank first.
	results, err = e.Search("container")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || string(results[0].TitleHTML) != "Understanding <mark>Container</mark> Networking" {
		t.Fatalf("results = %+v", results)
	}
	if got := string(results[1].Snippet); strings.Contains(got, "<mark>") || got == "" {
		t.Errorf("content snippet without a content match = %q, want unhighlighted opening text", got)
	}
}

// Posts are indexed with the Typographer's curly quotes and apostrophes. Like
// their ASCII forms they separate tokens, so words next to them are found
// whichever quote characters the query uses.
func TestSearchAroundTypography(t *testing.T) {
	post, err := blog.Parse([]byte("---\ntitle: \"Queues\"\n---\n\nThe worker's \"idempotent\" retries... aren't free -- they're 'cheap'.\n"))
	if err != nil {
		t.Fatal(err)
	}
	post.Slug = "queues"
	// (the no-break space binds the spaced dash to "free"; see blog.finish)
	if want := "The worker\u2019s \u201cidempotent\u201d retries\u2026 aren\u2019t free\u00a0\u2013 they\u2019re \u2018cheap\u2019."; post.PlainText != want {
		t.Fatalf("PlainText = %q, want %q", post.PlainText, want)
	}
	e := newTestSearch(t, *post)

	queries := []string{
		"idempotent", `"idempotent"`, "\u201cidempotent\u201d", "worker's", "worker\u2019s", "aren't",
		"aren\u2019t", "they're", "retries", "retries...", "cheap", "'cheap'", "\u2018cheap\u2019", "free",
	}
	for _, q := range queries {
		results, err := e.Search(q)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if len(results) != 1 {
			t.Errorf("Search(%q) found %d posts, want 1", q, len(results))
		}
	}

	results, err := e.Search("idempotent")
	if err != nil || len(results) != 1 {
		t.Fatalf("Search(idempotent) = %v, %v", results, err)
	}
	if got := string(results[0].Snippet); !strings.Contains(got, "\u201c<mark>idempotent</mark>\u201d") {
		t.Errorf("snippet = %q, want the highlighted word inside curly quotes", got)
	}
}

// A term under three characters matches whole words only: "go" finds Go and
// Go’s, never "good", "goes" or "goroutines", and highlights only Go.
func TestSearchShortTermsAreWholeWords(t *testing.T) {
	e := newTestSearch(t,
		models.Blog{Slug: "go", Title: "Concurrency in Go", PlainText: "Go’s goroutines are cheap."},
		models.Blog{Slug: "good", Title: "Good habits", PlainText: "Good code goes a long way."},
	)
	results, err := e.Search("go")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Slug != "go" {
		t.Fatalf("Search(go) = %+v, want only the Go post", results)
	}
	if got, want := string(results[0].Snippet), "<mark>Go</mark>’s goroutines are cheap."; got != want {
		t.Errorf("snippet = %q, want %q", got, want)
	}
	if got, want := string(results[0].TitleHTML), "Concurrency in <mark>Go</mark>"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
}

// Longer terms still match as prefixes, but a whole-word match ranks first.
func TestSearchWholeWordsRankFirst(t *testing.T) {
	filler := "lorem ipsum dolor sit amet"
	e := newTestSearch(t,
		models.Blog{Slug: "prefix", Title: filler, PlainText: "Postgres tuning. " + filler},
		models.Blog{Slug: "exact", Title: filler, PlainText: "One post a week. " + filler},
	)
	results, err := e.Search("post")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range results {
		got = append(got, r.Slug)
	}
	if strings.Join(got, ",") != "exact,prefix" {
		t.Errorf("Search(post) = %q, want the whole-word match first", got)
	}
}

// Snippets quote the prose, never a code listing: a post that matches only
// in its code shows the opening of its prose instead.
func TestSearchSnippetsSkipCode(t *testing.T) {
	e := newTestSearch(t, testBlogs...)
	results, err := e.Search("masquerade")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := string(results[0].Snippet); strings.Contains(got, "sudo") || strings.Contains(got, "<mark>") || !strings.HasPrefix(got, "Docker uses iptables") {
		t.Errorf("snippet = %q, want the unhighlighted opening of the prose", got)
	}

	// the real networking post: its prose and code are indexed separately
	post := realPost(t, "understanding-container-networking")
	e = newTestSearch(t, *post)
	for _, query := range []string{"veth", "bridge", "namespace"} {
		results, err := e.Search(query)
		if err != nil || len(results) != 1 {
			t.Fatalf("Search(%q) = %v, %v", query, results, err)
		}
		snippet := string(results[0].Snippet)
		for _, line := range strings.Split(post.CodeText, "\n") {
			if len(line) > 12 && strings.Contains(snippet, line) {
				t.Errorf("Search(%q) snippet quotes the code line %q: %s", query, line, snippet)
			}
		}
		if strings.Contains(snippet, "sudo") || strings.Contains(snippet, "# ") {
			t.Errorf("Search(%q) snippet looks like code: %s", query, snippet)
		}
	}
}

func realPost(t *testing.T, slug string) *models.Blog {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "content", "blogs", slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	post, err := blog.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	post.Slug = slug
	return post
}
