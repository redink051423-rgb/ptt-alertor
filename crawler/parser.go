package crawler

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ptt-alertor/ptt-alertor/models"
	"golang.org/x/net/html"
)

var (
	articleIDRegex = regexp.MustCompile(`M\.\d+\.A\.[A-Za-z0-9]+`)
)

// ParsePushCount converts PTT push sum representation to an integer
func ParsePushCount(str string) int {
	str = strings.TrimSpace(str)
	if str == "" {
		return 0
	}
	if str == "爆" {
		return 100
	}
	if str == "XX" {
		return -100
	}
	if strings.HasPrefix(str, "X") && len(str) == 2 {
		if digit, err := strconv.Atoi(string(str[1])); err == nil {
			return -digit * 10
		}
	}
	if n, err := strconv.Atoi(str); err == nil {
		return n
	}
	return 0
}

// ExtractArticleID extracts PTT article ID from link or text
func ExtractArticleID(link string) string {
	match := articleIDRegex.FindString(link)
	return match
}

// ParseIndexHTML parses the HTML of a board index page into a slice of articles
func ParseIndexHTML(board string, doc *html.Node) []models.Article {
	var articles []models.Article

	var findRent func(*html.Node)
	findRent = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "r-ent") {
			if art, ok := parseRentNode(board, n); ok {
				articles = append(articles, art)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findRent(c)
		}
	}
	findRent(doc)

	return articles
}

func parseRentNode(board string, n *html.Node) (models.Article, bool) {
	var art models.Article
	art.Board = board

	// 1. nrec (push count)
	nrecNode := findFirst(n, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "div" && hasClass(node, "nrec")
	})
	if nrecNode != nil {
		pushText := getText(nrecNode)
		art.PushSum = ParsePushCount(pushText)
	}

	// 2. title & link
	titleNode := findFirst(n, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "div" && hasClass(node, "title")
	})
	if titleNode == nil {
		return art, false
	}

	aNode := findFirst(titleNode, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "a"
	})
	if aNode == nil {
		// Article may be deleted or inaccessible
		return art, false
	}

	art.Title = strings.TrimSpace(getText(aNode))
	href := getAttr(aNode, "href")
	if href != "" {
		if !strings.HasPrefix(href, "http") {
			art.Link = "https://www.ptt.cc" + href
		} else {
			art.Link = href
		}
		art.ID = ExtractArticleID(href)
	}

	if art.ID == "" {
		return art, false
	}

	// 3. meta (author & date)
	metaNode := findFirst(n, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "div" && hasClass(node, "meta")
	})
	if metaNode != nil {
		authorNode := findFirst(metaNode, func(node *html.Node) bool {
			return node.Type == html.ElementNode && node.Data == "div" && hasClass(node, "author")
		})
		if authorNode != nil {
			art.Author = strings.TrimSpace(getText(authorNode))
		}

		dateNode := findFirst(metaNode, func(node *html.Node) bool {
			return node.Type == html.ElementNode && node.Data == "div" && hasClass(node, "date")
		})
		if dateNode != nil {
			art.Date = strings.TrimSpace(getText(dateNode))
		}
	}

	return art, true
}

func hasClass(n *html.Node, targetClass string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			classes := strings.Fields(attr.Val)
			for _, c := range classes {
				if c == targetClass {
					return true
				}
			}
		}
	}
	return false
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func getText(n *html.Node) string {
	var sb strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return sb.String()
}

func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := findFirst(c, match); res != nil {
			return res
		}
	}
	return nil
}
