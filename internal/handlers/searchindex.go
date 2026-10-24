package handlers

import "github.com/gofiber/fiber/v2"

// searchIndex is the payload of /search-index.json used by the command palette.
type searchIndex struct {
	Posts    []indexPost    `json:"posts"`
	Projects []indexProject `json:"projects"`
}

type indexPost struct {
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	URL         string   `json:"url"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Date        string   `json:"date"`
	ReadingTime int      `json:"readingTime"`
}

type indexProject struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Summary     string   `json:"summary"`
	Tech        []string `json:"tech"`
	GitHub      string   `json:"github"`
	Demo        string   `json:"demo"`
}

func (h *Handler) SearchIndex(c *fiber.Ctx) error {
	blogs := h.engine.GetAllBlogs()
	idx := searchIndex{
		Posts:    make([]indexPost, 0, len(blogs)),
		Projects: make([]indexProject, 0, len(h.projects)),
	}
	for _, b := range blogs {
		idx.Posts = append(idx.Posts, indexPost{
			Title:       b.Title,
			Slug:        b.Slug,
			URL:         "/blog/" + b.Slug,
			Description: b.Description,
			Tags:        nonNil(b.Tags),
			Date:        b.Date.Format("2006-01-02"),
			ReadingTime: b.ReadingTime,
		})
	}
	for _, p := range h.projects {
		idx.Projects = append(idx.Projects, indexProject{
			ID:          p.ID,
			Title:       p.Title,
			Description: p.Blurb(),
			Summary:     p.ShortSummary(),
			Tech:        nonNil(p.TechStack),
			GitHub:      p.GithubLink,
			Demo:        p.DemoLink,
		})
	}

	c.Set(fiber.HeaderCacheControl, "public, max-age=300")
	return c.JSON(idx)
}

// nonNil makes empty lists encode as [] instead of null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
