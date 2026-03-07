package blog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/sagnikb/myspace/internal/database"
	"github.com/sagnikb/myspace/internal/models"
)

type Engine struct {
	mu       sync.RWMutex
	blogs    map[string]*models.Blog // slug -> blog
	tagIndex map[string][]string     // tag -> slugs
	sorted   []*models.Blog          // sorted by date desc
	db       *database.DB
}

func NewEngine(db *database.DB) *Engine {
	return &Engine{
		blogs:    make(map[string]*models.Blog),
		tagIndex: make(map[string][]string),
		db:       db,
	}
}

func (e *Engine) LoadFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read content dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		blog, err := Parse(data)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		slug := strings.TrimSuffix(entry.Name(), ".md")
		blog.Slug = slug
		blog.ID = slug

		if blog.Status != "published" {
			continue
		}

		e.mu.Lock()
		e.blogs[slug] = blog

		for _, tag := range blog.Tags {
			e.tagIndex[tag] = append(e.tagIndex[tag], slug)
		}
		e.mu.Unlock()

		if e.db != nil {
			_ = e.db.UpsertBlog(*blog)
			_ = e.db.IndexBlog(*blog)
		}
	}

	e.rebuildSorted()
	return nil
}

func (e *Engine) rebuildSorted() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.sorted = make([]*models.Blog, 0, len(e.blogs))
	for _, b := range e.blogs {
		e.sorted = append(e.sorted, b)
	}
	sort.Slice(e.sorted, func(i, j int) bool {
		return e.sorted[i].Date.After(e.sorted[j].Date)
	})
}

func (e *Engine) GetBlog(slug string) *models.Blog {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.blogs[slug]
}

func (e *Engine) GetAllBlogs() []*models.Blog {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]*models.Blog, len(e.sorted))
	copy(result, e.sorted)
	return result
}

func (e *Engine) GetLatestBlogs(n int) []*models.Blog {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if n > len(e.sorted) {
		n = len(e.sorted)
	}
	result := make([]*models.Blog, n)
	copy(result, e.sorted[:n])
	return result
}

func (e *Engine) GetBlogsByTag(tag string) []*models.Blog {
	e.mu.RLock()
	defer e.mu.RUnlock()

	slugs := e.tagIndex[tag]
	var blogs []*models.Blog
	for _, slug := range slugs {
		if b, ok := e.blogs[slug]; ok {
			blogs = append(blogs, b)
		}
	}
	sort.Slice(blogs, func(i, j int) bool {
		return blogs[i].Date.After(blogs[j].Date)
	})
	return blogs
}

func (e *Engine) GetAllTags() []models.TagInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var tags []models.TagInfo
	for name, slugs := range e.tagIndex {
		tags = append(tags, models.TagInfo{Name: name, Count: len(slugs)})
	}
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Count > tags[j].Count
	})
	return tags
}

func (e *Engine) GetRelatedBlogs(slug string, limit int) []*models.Blog {
	e.mu.RLock()
	defer e.mu.RUnlock()

	current, ok := e.blogs[slug]
	if !ok {
		return nil
	}

	scores := make(map[string]int)
	for _, tag := range current.Tags {
		for _, s := range e.tagIndex[tag] {
			if s != slug {
				scores[s]++
			}
		}
	}

	type scored struct {
		slug  string
		score int
	}
	var ranked []scored
	for s, sc := range scores {
		ranked = append(ranked, scored{s, sc})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	if limit > len(ranked) {
		limit = len(ranked)
	}

	var related []*models.Blog
	for _, r := range ranked[:limit] {
		related = append(related, e.blogs[r.slug])
	}
	return related
}
