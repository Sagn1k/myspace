package models

import "time"

type Blog struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Content     string    `json:"-"`
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
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	TechStack   []string `json:"tech_stack"`
	GithubLink  string   `json:"github_link"`
	DemoLink    string   `json:"demo_link"`
	Featured    bool     `json:"featured"`
}

type TagInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type SearchResult struct {
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Tags        string  `json:"tags"`
	Score       float64 `json:"score"`
}

type SiteConfig struct {
	Title       string
	Description string
	Author      string
	Domain      string
	BaseURL     string
}

func DefaultConfig() SiteConfig {
	return SiteConfig{
		Title:       "Sagnik Bhowmick",
		Description: "Developer Portfolio & Knowledge Blog",
		Author:      "Sagnik Bhowmick",
		Domain:      "sagnikbhowmick.com",
		BaseURL:     "https://sagnikbhowmick.com",
	}
}
