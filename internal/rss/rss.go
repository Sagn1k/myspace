package rss

import (
	"encoding/xml"
	"fmt"
	"time"

	"github.com/sagnikb/myspace/internal/models"
)

type RSS struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel Channel  `xml:"channel"`
}

type Channel struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Language    string `xml:"language"`
	LastBuild   string `xml:"lastBuildDate"`
	Items       []Item `xml:"item"`
}

type Item struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
	Categories  []string `xml:"category"`
}

func Generate(blogs []*models.Blog, config models.SiteConfig) ([]byte, error) {
	var items []Item
	for _, b := range blogs {
		items = append(items, Item{
			Title:       b.Title,
			Link:        fmt.Sprintf("%s/blog/%s", config.BaseURL, b.Slug),
			Description: b.Description,
			PubDate:     b.Date.Format(time.RFC1123Z),
			GUID:        fmt.Sprintf("%s/blog/%s", config.BaseURL, b.Slug),
			Categories:  b.Tags,
		})
	}

	feed := RSS{
		Version: "2.0",
		Channel: Channel{
			Title:       config.Title,
			Link:        config.BaseURL,
			Description: config.Description,
			Language:    "en-us",
			LastBuild:   time.Now().Format(time.RFC1123Z),
			Items:       items,
		},
	}

	output, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}

	return append([]byte(xml.Header), output...), nil
}
