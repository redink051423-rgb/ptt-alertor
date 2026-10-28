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

type matchedAlert struct {
	Reason  string
	Article models.Article
}

type userSubGroup struct {
	Keywords []string
	Authors  []string
	PushSums []int
	Excludes []string
}

func (e *Engine) checkBoard(board string) {
	subs, err := e.db.GetBoardSubscribers(board)
	if err != nil || len(subs) == 0 {
		return
	}

	// Group subscriptions by user (lineID)
	userRules := make(map[string]*userSubGroup)
	for _, sub := range subs {
		if _, ok := userRules[sub.LineID]; !ok {
			userRules[sub.LineID] = &userSubGroup{}
		}
		ur := userRules[sub.LineID]
		switch sub.Type {
		case models.SubTypeKeyword:
			ur.Keywords = append(ur.Keywords, sub.Value)
		case models.SubTypeExcludeKeyword:
			ur.Excludes = append(ur.Excludes, sub.Value)
		case models.SubTypeAuthor:
			ur.Authors = append(ur.Authors, sub.Value)
		case models.SubTypePushSum:
			th := parseThreshold(sub.Value)
			if th > 0 {
				ur.PushSums = append(ur.PushSums, th)
			}
		}
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

	// Collect alerts per user to allow batching within this cycle
	userAlerts := make(map[string][]matchedAlert)

	for _, article := range articles {
		if article.ID == "" {
			continue
		}

		oldPushSum, seen, err := e.db.GetArticlePushSum(board, article.ID)
		if err != nil {
			continue
		}

		titleLower := strings.ToLower(article.Title)
		authorLower := strings.ToLower(article.Author)

		if !seen {
			// Brand new article detected
			for lineID, ur := range userRules {
				// 1. Negative keyword filter
				excluded := false
				for _, ex := range ur.Excludes {
					if strings.Contains(titleLower, strings.ToLower(ex)) {
						excluded = true
						break
					}
				}
				if excluded {
					continue
				}

				// 2. Keyword match
				matched := false
				var reason string
				for _, kw := range ur.Keywords {
					if strings.Contains(titleLower, strings.ToLower(kw)) {
						matched = true
						reason = "關鍵字【" + kw + "】"
						break
					}
				}

				// 3. Author match
				if !matched {
					for _, auth := range ur.Authors {
						if authorLower == strings.ToLower(auth) {
							matched = true
							reason = "作者【" + auth + "】"
							break
						}
					}
				}

				// 4. Push sum match
				if !matched {
					for _, th := range ur.PushSums {
						if article.PushSum >= th {
							matched = true
							reason = fmt.Sprintf("推文數達標【%d】", th)
							break
						}
					}
				}

				if matched {
					userAlerts[lineID] = append(userAlerts[lineID], matchedAlert{
						Reason:  reason,
						Article: article,
					})
				}
			}
			_ = e.db.RecordArticleSeen(board, article.ID, article.Title, article.Author, article.PushSum)
		} else if article.PushSum > oldPushSum {
			// Existing article with higher push count
			for lineID, ur := range userRules {
				excluded := false
				for _, ex := range ur.Excludes {
					if strings.Contains(titleLower, strings.ToLower(ex)) {
						excluded = true
						break
					}
				}
				if excluded {
					continue
				}

				for _, th := range ur.PushSums {
					if oldPushSum < th && article.PushSum >= th {
						userAlerts[lineID] = append(userAlerts[lineID], matchedAlert{
							Reason:  fmt.Sprintf("推文數達標【%d】", th),
							Article: article,
						})
						break
					}
				}
			}
			_ = e.db.RecordArticleSeen(board, article.ID, article.Title, article.Author, article.PushSum)
		}
	}

	// Dispatch alerts
	for lineID, alerts := range userAlerts {
		if len(alerts) == 0 {
			continue
		}
		if len(alerts) == 1 {
			msg := e.formatAlertMessage(alerts[0].Reason, alerts[0].Article)
			_ = e.bot.PushAlert(lineID, msg)
		} else {
			msg := e.formatBatchAlertMessage(board, alerts)
			_ = e.bot.PushAlert(lineID, msg)
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

func (e *Engine) formatAlertMessage(trigger string, art models.Article) string {
	canon := crawler.CanonicalBoard(art.Board)
	pushText := strconv.Itoa(art.PushSum)
	if art.PushSum >= 100 {
		pushText = "爆 (" + pushText + ")"
	} else if art.PushSum <= -100 {
		pushText = "XX (" + pushText + ")"
	}

	msg := fmt.Sprintf(`【PTT 文章通知】
看板：%s
觸發：%s
標題：%s
作者：%s
推文：%s
連結：%s`, canon, trigger, art.Title, art.Author, pushText, art.Link)

	// High-water mark alert (Option B): Append warning when remaining <= 20
	if quota, err := e.bot.GetQuota(false); err == nil && quota.Limited && quota.Total > 0 {
		if quota.Remaining <= 20 {
			msg += fmt.Sprintf("\n--------------------\n提醒：本月 LINE 免費額度僅剩 %d 則，接近上限。", quota.Remaining)
		}
	}

	return msg
}

func (e *Engine) formatBatchAlertMessage(board string, alerts []matchedAlert) string {
	canon := crawler.CanonicalBoard(board)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【PTT 文章通知（共 %d 篇）】\n看板：%s\n", len(alerts), canon))

	for i, al := range alerts {
		sb.WriteString(fmt.Sprintf("\n%d. %s\n觸發：%s\n作者：%s\n連結：%s\n", i+1, al.Article.Title, al.Reason, al.Article.Author, al.Article.Link))
	}

	// High-water mark alert (Option B): Append warning when remaining <= 20
	if quota, err := e.bot.GetQuota(false); err == nil && quota.Limited && quota.Total > 0 {
		if quota.Remaining <= 20 {
			sb.WriteString(fmt.Sprintf("\n--------------------\n提醒：本月 LINE 免費額度僅剩 %d 則，接近上限。", quota.Remaining))
		}
	}

	return strings.TrimSpace(sb.String())
}
