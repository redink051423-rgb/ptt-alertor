package engine

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ptt-alertor/ptt-alertor/bot"
	"github.com/ptt-alertor/ptt-alertor/crawler"
	"github.com/ptt-alertor/ptt-alertor/models"
	"github.com/ptt-alertor/ptt-alertor/storage"
)

type Engine struct {
	db               *storage.DB
	crawler          *crawler.Crawler
	bot              *bot.LineBot
	checkInterval    time.Duration
	cleanupInterval  time.Duration
}

func NewEngine(db *storage.DB, c *crawler.Crawler, b *bot.LineBot, checkIntervalSec, cleanupIntervalHr int) *Engine {
	return &Engine{
		db:              db,
		crawler:         c,
		bot:             b,
		checkInterval:   time.Duration(checkIntervalSec) * time.Second,
		cleanupInterval: time.Duration(cleanupIntervalHr) * time.Hour,
	}
}

// Start launches the checker and cleanup goroutines
func (e *Engine) Start(ctx context.Context) {
	log.Printf("Starting monitoring engine (poll interval: %v)", e.checkInterval)
	go e.runCheckerLoop(ctx)
	go e.runCleanupLoop(ctx)
}

func (e *Engine) runCheckerLoop(ctx context.Context) {
	ticker := time.NewTicker(e.checkInterval)
	defer ticker.Stop()

	// Initial check on start
	e.checkAllBoards()

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping monitoring engine...")
			return
		case <-ticker.C:
			e.checkAllBoards()
		}
	}
}

func (e *Engine) runCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(e.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.Println("Running daily article cleanup (retention: 7 days)...")
			if err := e.db.CleanOldArticles(7); err != nil {
				log.Printf("CleanOldArticles error: %v", err)
			}
		}
	}
}

func (e *Engine) checkAllBoards() {
	boards, err := e.db.GetDistinctSubscribedBoards()
	if err != nil {
		log.Printf("GetDistinctSubscribedBoards error: %v", err)
		return
	}
	if len(boards) == 0 {
		return
	}

	var wg sync.WaitGroup
	// Limit concurrent scraping to 5 boards to be polite to PTT
	sem := make(chan struct{}, 5)

	for _, b := range boards {
		wg.Add(1)
		sem <- struct{}{}
		go func(boardName string) {
			defer wg.Done()
			defer func() { <-sem }()
			e.checkBoard(boardName)
		}(b)
	}

	wg.Wait()
}

func (e *Engine) checkBoard(board string) {
	subs, err := e.db.GetBoardSubscribers(board)
	if err != nil || len(subs) == 0 {
		return
	}

	articles, err := e.crawler.FetchArticles(board)
	if err != nil {
		// Attempt RSS fallback if HTML fails
		rssArticles, rssErr := e.crawler.FetchArticlesRSS(board)
		if rssErr != nil {
			log.Printf("Failed to crawl board %s: %v (rss: %v)", board, err, rssErr)
			return
		}
		articles = rssArticles
	}

	for _, article := range articles {
		if article.ID == "" {
			continue
		}

		oldPushSum, seen, err := e.db.GetArticlePushSum(board, article.ID)
		if err != nil {
			continue
		}

		if !seen {
			// Brand new article detected
			e.matchNewArticle(article, subs)
			_ = e.db.RecordArticleSeen(board, article.ID, article.Title, article.Author, article.PushSum)
		} else if article.PushSum > oldPushSum {
			// Existing article with higher push count
			e.matchPushSumIncrease(article, oldPushSum, subs)
			_ = e.db.RecordArticleSeen(board, article.ID, article.Title, article.Author, article.PushSum)
		}
	}
}

func (e *Engine) matchNewArticle(art models.Article, subs []models.Subscription) {
	titleLower := strings.ToLower(art.Title)
	authorLower := strings.ToLower(art.Author)

	for _, sub := range subs {
		switch sub.Type {
		case models.SubTypeKeyword:
			kw := strings.ToLower(sub.Value)
			if strings.Contains(titleLower, kw) {
				msg := formatAlertMessage("關鍵字【"+sub.Value+"】", art)
				_ = e.bot.PushAlert(sub.LineID, msg)
			}

		case models.SubTypeAuthor:
			targetAuthor := strings.ToLower(sub.Value)
			if authorLower == targetAuthor {
				msg := formatAlertMessage("作者【"+sub.Value+"】", art)
				_ = e.bot.PushAlert(sub.LineID, msg)
			}

		case models.SubTypePushSum:
			threshold := parseThreshold(sub.Value)
			if threshold > 0 && art.PushSum >= threshold {
				msg := formatAlertMessage("推文數達標【"+sub.Value+"】", art)
				_ = e.bot.PushAlert(sub.LineID, msg)
			}
		}
	}
}

func (e *Engine) matchPushSumIncrease(art models.Article, oldPushSum int, subs []models.Subscription) {
	for _, sub := range subs {
		if sub.Type == models.SubTypePushSum {
			threshold := parseThreshold(sub.Value)
			// Trigger only when it crosses the threshold in this update
			if threshold > 0 && oldPushSum < threshold && art.PushSum >= threshold {
				msg := formatAlertMessage("推文數達標【"+sub.Value+"】", art)
				_ = e.bot.PushAlert(sub.LineID, msg)
			}
		}
	}
}

func parseThreshold(val string) int {
	if val == "爆" {
		return 100
	}
	if n, err := strconv.Atoi(val); err == nil {
		return n
	}
	return 0
}

func formatAlertMessage(trigger string, art models.Article) string {
	pushText := strconv.Itoa(art.PushSum)
	if art.PushSum >= 100 {
		pushText = "爆 (" + pushText + ")"
	} else if art.PushSum <= -100 {
		pushText = "XX (" + pushText + ")"
	}

	return fmt.Sprintf(`【PTT 文章通知】
看板：%s
觸發：%s
標題：%s
作者：%s
推文：%s
連結：%s`, art.Board, trigger, art.Title, art.Author, pushText, art.Link)
}
