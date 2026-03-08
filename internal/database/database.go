package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagnikb/myspace/internal/models"
	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

func New(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
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
		`CREATE VIRTUAL TABLE IF NOT EXISTS blogs_fts USING fts5(
			slug,
			title,
			description,
			tags,
			content
		)`,
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

func (db *DB) IndexBlog(b models.Blog) error {
	_, _ = db.conn.Exec(`DELETE FROM blogs_fts WHERE slug = ?`, b.Slug)
	_, err := db.conn.Exec(`
		INSERT INTO blogs_fts (slug, title, description, tags, content)
		VALUES (?, ?, ?, ?, ?)`,
		b.Slug, b.Title, b.Description,
		strings.Join(b.Tags, " "), b.Content,
	)
	return err
}

func (db *DB) Search(query string) ([]models.SearchResult, error) {
	rows, err := db.conn.Query(`
		SELECT slug, title, description, tags, rank
		FROM blogs_fts
		WHERE blogs_fts MATCH ?
		ORDER BY rank
		LIMIT 20`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.SearchResult
	for rows.Next() {
		var r models.SearchResult
		if err := rows.Scan(&r.Slug, &r.Title, &r.Description, &r.Tags, &r.Score); err != nil {
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
