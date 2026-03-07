package search

import (
	"github.com/sagnikb/myspace/internal/database"
	"github.com/sagnikb/myspace/internal/models"
)

type Engine struct {
	db *database.DB
}

func New(db *database.DB) *Engine {
	return &Engine{db: db}
}

func (e *Engine) Search(query string) ([]models.SearchResult, error) {
	if query == "" {
		return nil, nil
	}
	return e.db.Search(query)
}
