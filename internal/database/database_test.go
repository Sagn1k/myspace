package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sagnikb/myspace/internal/models"
)

// DB_PATH=:memory: must behave like a file: every query sees the same data,
// even when requests run concurrently.
func TestInMemoryConcurrentSearch(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.IndexBlog(models.Blog{Slug: "a", Title: "Go tips", PlainText: "go"}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			results, err := db.Search(`"go"*`, 20)
			if err == nil && len(results) != 1 {
				t.Errorf("got %d results, want 1", len(results))
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent search: %v", err)
		}
	}
}

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(filepath.Join(t.TempDir(), "nested", "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func searchSlugs(t *testing.T, db *DB, match string) []string {
	t.Helper()
	results, err := db.Search(match, 20)
	if err != nil {
		t.Fatalf("Search(%q): %v", match, err)
	}
	var slugs []string
	for _, r := range results {
		slugs = append(slugs, r.Slug)
	}
	return slugs
}

func TestIndexBlogReplacesEntry(t *testing.T) {
	db := newTestDB(t)
	b := models.Blog{Slug: "post", Title: "Post", PlainText: "original words"}
	if err := db.IndexBlog(b); err != nil {
		t.Fatal(err)
	}
	b.PlainText = "rewritten text"
	if err := db.IndexBlog(b); err != nil {
		t.Fatal(err)
	}

	if got := searchSlugs(t, db, `"original"`); len(got) != 0 {
		t.Errorf("stale content still indexed: %q", got)
	}
	if got := searchSlugs(t, db, `"rewritten"`); strings.Join(got, ",") != "post" {
		t.Errorf("Search(rewritten) = %q", got)
	}
}

func TestSearchColumnWeights(t *testing.T) {
	db := newTestDB(t)
	filler := "lorem ipsum dolor sit amet"
	blogs := []models.Blog{
		{Slug: "in-content", Title: filler, Description: filler, Tags: []string{"misc"}, PlainText: "gopher " + filler},
		{Slug: "in-description", Title: filler, Description: "gopher " + filler, Tags: []string{"misc"}, PlainText: filler},
		{Slug: "in-tags", Title: filler, Description: filler, Tags: []string{"gopher", "misc"}, PlainText: filler},
		{Slug: "in-title", Title: "gopher " + filler, Description: filler, Tags: []string{"misc"}, PlainText: filler},
		{Slug: "in-code", Title: filler, Description: filler, Tags: []string{"misc"}, PlainText: filler, CodeText: "gopher := 1"},
		{Slug: "gopher-in-slug-only", Title: filler, Description: filler, Tags: []string{"misc"}, PlainText: filler},
	}
	for _, b := range blogs {
		if err := db.IndexBlog(b); err != nil {
			t.Fatal(err)
		}
	}

	got := strings.Join(searchSlugs(t, db, `"gopher"*`), ",")
	want := "in-title,in-tags,in-description,in-content,in-code,gopher-in-slug-only"
	if got != want {
		t.Errorf("rank order = %s, want %s", got, want)
	}
}

func TestSearchMarkers(t *testing.T) {
	db := newTestDB(t)
	if err := db.IndexBlog(models.Blog{Slug: "a", Title: "Go tips", PlainText: "Some go code."}); err != nil {
		t.Fatal(err)
	}
	results, err := db.Search(`"go"*`, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	r := results[0]
	if r.Title != MarkStart+"Go"+MarkEnd+" tips" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Snippet != "Some "+MarkStart+"go"+MarkEnd+" code." {
		t.Errorf("Snippet = %q", r.Snippet)
	}
}

// A database file from a version whose search index had no code column gets
// the current index: the old one is dropped (it is rebuilt from the posts at
// every start), and code is searchable again.
func TestMigrateOutdatedSearchIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE VIRTUAL TABLE blogs_fts USING fts5(slug, title, description, tags, content)`,
		`INSERT INTO blogs_fts VALUES ('stale', 'Stale', '', '', 'gopher')`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	db, err := New(path)
	if err != nil {
		t.Fatalf("New on an outdated index: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	var cols []string
	rows, err := db.conn.Query(`SELECT name FROM pragma_table_info('blogs_fts')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	if got := strings.Join(cols, ", "); got != ftsColumns {
		t.Errorf("columns = %q, want %q", got, ftsColumns)
	}
	if got := searchSlugs(t, db, `"gopher"`); len(got) != 0 {
		t.Errorf("the outdated index's rows survived: %q", got)
	}
	if err := db.IndexBlog(models.Blog{Slug: "post", Title: "Post", PlainText: "prose", CodeText: "gopher := 1"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(searchSlugs(t, db, `"gopher"`), ","); got != "post" {
		t.Errorf("Search(gopher) = %q, want post", got)
	}

	// and opening the current index again keeps it
	db.Close()
	again, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { again.Close() })
	if got := strings.Join(searchSlugs(t, again, `"gopher"`), ","); got != "post" {
		t.Errorf("after reopening, Search(gopher) = %q, want post", got)
	}
}
