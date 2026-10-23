package crawler

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ptt-alertor/ptt-alertor/models"
	"golang.org/x/net/html"
)

type Crawler struct {
	client *http.Client
}

func NewCrawler() *Crawler {
	return &Crawler{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Crawler) newRequest(method, urlStr string) (*http.Request, error) {
	req, err := http.NewRequest(method, urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Cookie", "over18=1")
	return req, nil
}

// CheckBoardExist verifies if a PTT board exists
func (c *Crawler) CheckBoardExist(board string) bool {
	board = strings.TrimSpace(board)
	urlStr := fmt.Sprintf("https://www.ptt.cc/bbs/%s/index.html", board)
	req, err := c.newRequest("GET", urlStr)
	if err != nil {
		return false
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// FetchArticles fetches recent articles from the board's index page
func (c *Crawler) FetchArticles(board string) ([]models.Article, error) {
	board = strings.TrimSpace(board)
	urlStr := fmt.Sprintf("https://www.ptt.cc/bbs/%s/index.html", board)
	req, err := c.newRequest("GET", urlStr)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch board %s: %w", board, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("board %s returned status %d", board, resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse html for board %s: %w", board, err)
	}

	articles := ParseIndexHTML(board, doc)
	return articles, nil
}

// Atom XML structures for PTT RSS
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	ID        string     `xml:"id"`
	Published string     `xml:"published"`
	Author    atomAuthor `xml:"author"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

// FetchArticlesRSS fetches recent articles using PTT Atom RSS
func (c *Crawler) FetchArticlesRSS(board string) ([]models.Article, error) {
	board = strings.TrimSpace(board)
	urlStr := fmt.Sprintf("https://www.ptt.cc/atom/%s.xml", board)
	req, err := c.newRequest("GET", urlStr)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rss returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, err
	}

	var articles []models.Article
	for _, entry := range feed.Entries {
		id := ExtractArticleID(entry.ID)
		if id == "" {
			continue
		}
		articles = append(articles, models.Article{
			ID:     id,
			Board:  board,
			Title:  strings.TrimSpace(entry.Title),
			Author: strings.TrimSpace(entry.Author.Name),
			Link:   entry.ID,
			Date:   entry.Published,
		})
	}

	return articles, nil
}
