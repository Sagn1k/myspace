package blog

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sagnikb/myspace/internal/models"
)

// newTestEngine loads posts written as slug -> {date, tags, status}.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	posts := []struct {
		slug, date, tags, status string
	}{
		{"alpha", "2026-03-01", `["go", "web"]`, "published"},
		{"bravo", "2026-02-01", `["go", "db"]`, "published"},
		{"charlie", "2026-02-01", `["go", "web", "db"]`, "published"},
		{"delta", "2025-12-15", `["rust"]`, "published"},
		{"echo", "2026-04-01", `["go"]`, "draft"},
	}
	dir := t.TempDir()
	for _, p := range posts {
		src := fmt.Sprintf("---\ntitle: %q\ndate: %s\ntags: %s\nstatus: %s\n---\n\n## Heading\n\nBody of %s.\n",
			p.slug, p.date, p.tags, p.status, p.slug)
		if err := os.WriteFile(filepath.Join(dir, p.slug+".md"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(nil)
	if err := e.LoadFromDir(dir); err != nil {
		t.Fatal(err)
	}
	return e
}

func slugs(blogs []*models.Blog) []string {
	out := make([]string, 0, len(blogs))
	for _, b := range blogs {
		out = append(out, b.Slug)
	}
	return out
}

func slugOf(b *models.Blog) string {
	if b == nil {
		return ""
	}
	return b.Slug
}

func TestEngineOrdering(t *testing.T) {
	e := newTestEngine(t)

	tests := []struct {
		name string
		got  func() []string
		want []string
	}{
		{"all blogs newest first, ties by slug", func() []string { return slugs(e.GetAllBlogs()) }, []string{"alpha", "bravo", "charlie", "delta"}},
		{"latest 2", func() []string { return slugs(e.GetLatestBlogs(2)) }, []string{"alpha", "bravo"}},
		{"latest more than available", func() []string { return slugs(e.GetLatestBlogs(10)) }, []string{"alpha", "bravo", "charlie", "delta"}},
		{"latest negative", func() []string { return slugs(e.GetLatestBlogs(-1)) }, []string{}},
		{"by tag", func() []string { return slugs(e.GetBlogsByTag("go")) }, []string{"alpha", "bravo", "charlie"}},
		{"by unknown tag", func() []string { return slugs(e.GetBlogsByTag("nope")) }, []string{}},
		{"related by shared tags", func() []string { return slugs(e.GetRelatedBlogs("alpha", 3)) }, []string{"charlie", "bravo"}},
		{"related ties broken by date", func() []string { return slugs(e.GetRelatedBlogs("charlie", 3)) }, []string{"alpha", "bravo"}},
		{"related limit", func() []string { return slugs(e.GetRelatedBlogs("charlie", 1)) }, []string{"alpha"}},
		{"related none shared", func() []string { return slugs(e.GetRelatedBlogs("delta", 3)) }, []string{}},
		{"related unknown slug", func() []string { return slugs(e.GetRelatedBlogs("nope", 3)) }, []string{}},
		{"related negative limit", func() []string { return slugs(e.GetRelatedBlogs("alpha", -1)) }, []string{}},
		{"suggested: related first, then the rest newest first", func() []string { return slugs(e.GetSuggestedBlogs("alpha", 5)) }, []string{"charlie", "bravo", "delta"}},
		{"suggested without shared tags is newest first", func() []string { return slugs(e.GetSuggestedBlogs("delta", 5)) }, []string{"alpha", "bravo", "charlie"}},
		{"suggested limit", func() []string { return slugs(e.GetSuggestedBlogs("bravo", 2)) }, []string{"charlie", "alpha"}},
		{"suggested zero limit", func() []string { return slugs(e.GetSuggestedBlogs("bravo", 0)) }, []string{}},
		{"suggested negative limit", func() []string { return slugs(e.GetSuggestedBlogs("bravo", -1)) }, []string{}},
		{"suggested unknown slug", func() []string { return slugs(e.GetSuggestedBlogs("nope", 5)) }, []string{}},
		{"suggested skips drafts", func() []string { return slugs(e.GetSuggestedBlogs("charlie", 10)) }, []string{"alpha", "bravo", "delta"}},
		{"tags by count then name", func() []string {
			var out []string
			for _, tag := range e.GetAllTags() {
				out = append(out, fmt.Sprintf("%s:%d", tag.Name, tag.Count))
			}
			return out
		}, []string{"go:3", "db:2", "web:2", "rust:1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Map iteration order is randomised; repeat to catch nondeterminism.
			for i := 0; i < 20; i++ {
				if got := tt.got(); !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("run %d: got %q, want %q", i, got, tt.want)
				}
			}
		})
	}
}

func TestGetAdjacentBlogs(t *testing.T) {
	e := newTestEngine(t)
	tests := []struct {
		slug, newer, older string
	}{
		{"alpha", "", "bravo"},
		{"bravo", "alpha", "charlie"},
		{"charlie", "bravo", "delta"},
		{"delta", "charlie", ""},
		{"echo", "", ""},
		{"nope", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			newer, older := e.GetAdjacentBlogs(tt.slug)
			if slugOf(newer) != tt.newer || slugOf(older) != tt.older {
				t.Errorf("GetAdjacentBlogs(%q) = (%q, %q), want (%q, %q)",
					tt.slug, slugOf(newer), slugOf(older), tt.newer, tt.older)
			}
		})
	}
}

func TestGetBlogsByYear(t *testing.T) {
	e := newTestEngine(t)
	var got []string
	for _, g := range e.GetBlogsByYear() {
		got = append(got, fmt.Sprintf("%d:%v", g.Year, slugs(g.Posts)))
	}
	want := []string{"2026:[alpha bravo charlie]", "2025:[delta]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetBlogsByYear = %q, want %q", got, want)
	}
}

func TestGetPullquoteBlog(t *testing.T) {
	if b := newTestEngine(t).GetPullquoteBlog(); b != nil {
		t.Errorf("GetPullquoteBlog() = %q, want nil when no post has a pullquote", b.Slug)
	}

	dir := t.TempDir()
	posts := map[string]string{
		"old":   "date: 2025-01-01\npullquote: \"Old line.\"\nstatus: published",
		"new":   "date: 2026-01-01\npullquote: \"New line.\"\nstatus: published",
		"plain": "date: 2026-06-01\nstatus: published",
		"draft": "date: 2026-09-01\npullquote: \"Draft line.\"\nstatus: draft",
	}
	for slug, fm := range posts {
		src := fmt.Sprintf("---\ntitle: %q\n%s\n---\n\nBody.\n", slug, fm)
		if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := NewEngine(nil)
	if err := e.LoadFromDir(dir); err != nil {
		t.Fatal(err)
	}
	b := e.GetPullquoteBlog()
	if b == nil || b.Slug != "new" || b.Pullquote != "New line." {
		t.Errorf("GetPullquoteBlog() = %+v, want the newest published post with a pullquote", b)
	}
}

func TestDraftsAreExcluded(t *testing.T) {
	e := newTestEngine(t)
	if e.GetBlog("echo") != nil {
		t.Error("draft post is published")
	}
	for _, tag := range e.GetAllTags() {
		if tag.Name == "go" && tag.Count != 3 {
			t.Errorf("draft counted in tag go: %d", tag.Count)
		}
	}
}
