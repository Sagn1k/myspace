package blog

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/sagnikb/myspace/internal/database"
	"github.com/sagnikb/myspace/internal/models"
)

type Engine struct {
	mu       sync.RWMutex
	blogs    map[string]*models.Blog // slug -> blog
	tagIndex map[string][]string     // tag -> slugs
	sorted   []*models.Blog          // newest first, then slug
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
			if err := e.db.UpsertBlog(*blog); err != nil {
				return fmt.Errorf("store %s: %w", slug, err)
			}
			if err := e.db.IndexBlog(*blog); err != nil {
				return fmt.Errorf("index %s: %w", slug, err)
			}
		}
	}

	e.rebuildSorted()
	return nil
}

// newestFirst orders posts by date descending, breaking ties by slug so the
// order never depends on map iteration.
func newestFirst(a, b *models.Blog) int {
	if c := b.Date.Compare(a.Date); c != 0 {
		return c
	}
	return strings.Compare(a.Slug, b.Slug)
}

func (e *Engine) rebuildSorted() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.sorted = make([]*models.Blog, 0, len(e.blogs))
	for _, b := range e.blogs {
		e.sorted = append(e.sorted, b)
	}
	slices.SortFunc(e.sorted, newestFirst)
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
	n = max(0, min(n, len(e.sorted)))
	result := make([]*models.Blog, n)
	copy(result, e.sorted[:n])
	return result
}

// GetPullquoteBlog returns the newest post with a pullquote, or nil.
func (e *Engine) GetPullquoteBlog() *models.Blog {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, b := range e.sorted {
		if b.Pullquote != "" {
			return b
		}
	}
	return nil
}

// GetAdjacentBlogs returns the posts published right after (newer) and right
// before (older) slug. Either is nil at the ends of the list or when slug is
// unknown.
func (e *Engine) GetAdjacentBlogs(slug string) (newer, older *models.Blog) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for i, b := range e.sorted {
		if b.Slug != slug {
			continue
		}
		if i > 0 {
			newer = e.sorted[i-1]
		}
		if i+1 < len(e.sorted) {
			older = e.sorted[i+1]
		}
		break
	}
	return newer, older
}

// GetBlogsByYear groups all posts by publication year, newest year first.
func (e *Engine) GetBlogsByYear() []models.YearGroup {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var groups []models.YearGroup
	for _, b := range e.sorted {
		year := b.Date.Year()
		if n := len(groups); n == 0 || groups[n-1].Year != year {
			groups = append(groups, models.YearGroup{Year: year})
		}
		last := &groups[len(groups)-1]
		last.Posts = append(last.Posts, b)
	}
	return groups
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
	slices.SortFunc(blogs, newestFirst)
	return blogs
}

// GetAllTags returns every tag ordered by post count descending, then name.
func (e *Engine) GetAllTags() []models.TagInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tags := make([]models.TagInfo, 0, len(e.tagIndex))
	for name, slugs := range e.tagIndex {
		tags = append(tags, models.TagInfo{Name: name, Count: len(slugs)})
	}
	slices.SortFunc(tags, func(a, b models.TagInfo) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return tags
}

// GetRelatedBlogs returns up to limit posts sharing tags with slug, ordered by
// number of shared tags, then newest first, then slug.
func (e *Engine) GetRelatedBlogs(slug string, limit int) []*models.Blog {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var related []*models.Blog
	for _, o := range e.othersByRelevance(slug) {
		if len(related) >= limit || o.shared == 0 {
			break
		}
		related = append(related, o.blog)
	}
	return related
}

// GetSuggestedBlogs returns up to limit posts to read after slug: the posts
// GetRelatedBlogs ranks first, then every other post, newest first. Unlike
// GetRelatedBlogs it always returns min(limit, posts-1) posts for a known
// slug, so a "keep reading" list fills whenever the site has enough posts.
func (e *Engine) GetSuggestedBlogs(slug string, limit int) []*models.Blog {
	e.mu.RLock()
	defer e.mu.RUnlock()

	others := e.othersByRelevance(slug)
	suggested := make([]*models.Blog, max(0, min(limit, len(others))))
	for i := range suggested {
		suggested[i] = others[i].blog
	}
	return suggested
}

type scoredBlog struct {
	blog   *models.Blog
	shared int // tags in common with the reference post
}

// othersByRelevance returns every post except slug, most shared tags first,
// then newest first, then slug; nil when slug is unknown. Callers must hold
// e.mu.
func (e *Engine) othersByRelevance(slug string) []scoredBlog {
	current, ok := e.blogs[slug]
	if !ok {
		return nil
	}
	shared := make(map[string]int)
	for _, tag := range current.Tags {
		for _, s := range e.tagIndex[tag] {
			shared[s]++
		}
	}
	others := make([]scoredBlog, 0, len(e.sorted))
	for _, b := range e.sorted {
		if b.Slug != slug {
			others = append(others, scoredBlog{blog: b, shared: shared[b.Slug]})
		}
	}
	// e.sorted is newest first (then slug), and a stable sort keeps that
	// order among posts with the same number of shared tags.
	slices.SortStableFunc(others, func(a, b scoredBlog) int {
		return cmp.Compare(b.shared, a.shared)
	})
	return others
}
