package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagnikb/myspace/internal/models"
	_ "modernc.org/sqlite"
)

// Search wraps matched terms in these control characters. They cannot occur
// in markup, so callers can HTML-escape the text first and then turn them
// into tags.
const (
	MarkStart = "\x02"
	MarkEnd   = "\x03"
)

// ftsColumns are blogs_fts's columns in order: content is a post's prose
// (Blog.PlainText) and code the text of its code blocks (Blog.CodeText).
const ftsColumns = "slug, title, description, tags, content, code"

// searchSQL ranks with bm25 weighted per blogs_fts column (ftsColumns):
// title > tags > description > content > code. Snippets come from the prose
// only, so a result never quotes a code listing; a post that matches only in
// code shows the opening of its prose.
const searchSQL = `
	SELECT slug,
		highlight(blogs_fts, 1, ?1, ?2),
		snippet(blogs_fts, 4, ?1, ?2, '…', 32),
		bm25(blogs_fts, 0.0, 10.0, 3.0, 5.0, 1.0, 0.5) AS score
	FROM blogs_fts
	WHERE blogs_fts MATCH ?3
	ORDER BY score, slug
	LIMIT ?4`

type DB struct {
	conn *sql.DB
}

func New(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// SQLite allows a single writer, and every pooled connection to
	// DB_PATH=:memory: would get its own empty database, so use one connection.
	conn.SetMaxOpenConns(1)

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	// The full-text index is derived data, rebuilt from content/blogs at
	// every start (IndexBlog). An index an older version built with other
	// columns is dropped, so the CREATE below makes the current one.
	var cols []string
	rows, err := db.conn.Query(`SELECT name FROM pragma_table_info('blogs_fts')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		cols = append(cols, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(cols) > 0 && strings.Join(cols, ", ") != ftsColumns {
		if _, err := db.conn.Exec(`DROP TABLE blogs_fts`); err != nil {
			return fmt.Errorf("drop outdated search index: %w", err)
		}
	}

	queries := []string{
		`CREATE TABLE IF NOT EXISTS blogs (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			slug TEXT UNIQUE NOT NULL,
			description TEXT,
			published_date TEXT,
			reading_time INTEGER,
			tags TEXT,
			status TEXT DEFAULT 'published'
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT,
			tech_stack TEXT,
			github_link TEXT,
			demo_link TEXT,
			featured INTEGER DEFAULT 0
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS blogs_fts USING fts5(` + ftsColumns + `)`,
	}

	for _, q := range queries {
		if _, err := db.conn.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) UpsertBlog(b models.Blog) error {
	_, err := db.conn.Exec(`
		INSERT INTO blogs (id, title, slug, description, published_date, reading_time, tags, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			title=excluded.title,
			description=excluded.description,
			published_date=excluded.published_date,
			reading_time=excluded.reading_time,
			tags=excluded.tags,
			status=excluded.status`,
		b.Slug, b.Title, b.Slug, b.Description,
		b.Date.Format("2006-01-02"), b.ReadingTime,
		strings.Join(b.Tags, ","), b.Status,
	)
	return err
}

// IndexBlog replaces the post's full-text entry with its prose and code.
func (db *DB) IndexBlog(b models.Blog) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM blogs_fts WHERE slug = ?`, b.Slug); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO blogs_fts (`+ftsColumns+`)
		VALUES (?, ?, ?, ?, ?, ?)`,
		b.Slug, b.Title, b.Description,
		strings.Join(b.Tags, " "), b.PlainText, b.CodeText,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// Search runs an FTS5 MATCH expression. The expression must already be safe
// FTS5 syntax; build it from user input with the search package.
func (db *DB) Search(match string, limit int) ([]models.SearchResult, error) {
	rows, err := db.conn.Query(searchSQL, MarkStart, MarkEnd, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.SearchResult
	for rows.Next() {
		var r models.SearchResult
		if err := rows.Scan(&r.Slug, &r.Title, &r.Snippet, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (db *DB) UpsertProject(p models.Project) error {
	featured := 0
	if p.Featured {
		featured = 1
	}
	_, err := db.conn.Exec(`
		INSERT INTO projects (id, title, description, tech_stack, github_link, demo_link, featured)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title,
			description=excluded.description,
			tech_stack=excluded.tech_stack,
			github_link=excluded.github_link,
			demo_link=excluded.demo_link,
			featured=excluded.featured`,
		p.ID, p.Title, p.Description,
		strings.Join(p.TechStack, ","),
		p.GithubLink, p.DemoLink, featured,
	)
	return err
}
