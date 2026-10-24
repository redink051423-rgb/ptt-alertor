package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ptt-alertor/ptt-alertor/bot"
	"github.com/ptt-alertor/ptt-alertor/config"
	"github.com/ptt-alertor/ptt-alertor/crawler"
	"github.com/ptt-alertor/ptt-alertor/engine"
	"github.com/ptt-alertor/ptt-alertor/storage"
)

var startTime = time.Now()

func main() {
	log.Println("Starting PTT Alertor...")
	cfg := config.Load()

	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}
	if cfg.LineChannelSecret == "" || cfg.LineChannelAccessToken == "" {
		log.Fatal("LINE_CHANNEL_SECRET and LINE_CHANNEL_ACCESS_TOKEN are required")
	}

	// 1. Storage
	db, err := storage.NewDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()
	log.Println("Connected to PostgreSQL successfully")

	// 2. Crawler & Command Handler
	pttCrawler := crawler.NewCrawler()
	cmdHandler := bot.NewCommandHandler(db, pttCrawler)

	// 3. LINE Bot
	lineBot, err := bot.NewLineBot(cfg.LineChannelSecret, cfg.LineChannelAccessToken, cmdHandler, db)
	if err != nil {
		log.Fatalf("LineBot initialization failed: %v", err)
	}

	// 4. Background Monitoring Engine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng := engine.NewEngine(db, pttCrawler, lineBot, cfg.CheckIntervalSec, cfg.CleanupIntervalHr)
	eng.Start(ctx)

	// 5. HTTP Router
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"service": "PTT Alertor",
			"status":  "running",
			"uptime":  time.Since(startTime).String(),
		})
	})

	// Health check endpoint for UptimeRobot / Render keep-alive
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
			"uptime": time.Since(startTime).String(),
		})
	})

	// Debug check endpoint to inspect PTT response from Render
	mux.HandleFunc("/debug/check", func(w http.ResponseWriter, r *http.Request) {
		board := r.URL.Query().Get("b")
		if board == "" {
			board = "Stock"
		}

		// Raw test
		req, _ := http.NewRequest("GET", "https://www.ptt.cc/bbs/"+board+"/index.html", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Cookie", "over18=1")
		resp, err := http.DefaultClient.Do(req)
		var rawStatus int
		var rawBody string
		if err == nil {
			rawStatus = resp.StatusCode
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if len(b) > 400 {
				rawBody = string(b[:400])
			} else {
				rawBody = string(b)
			}
		}

		arts, err := pttCrawler.FetchArticles(board)
		rssArts, rssErr := pttCrawler.FetchArticlesRSS(board)

		// Test curl atom from container
		curlOut, curlErr := exec.Command("curl", "-s", "-i", "-b", "over18=1", "-A", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36", "https://www.ptt.cc/atom/"+board+".xml").CombinedOutput()
		var curlSnippet string
		if len(curlOut) > 300 {
			curlSnippet = string(curlOut[:300])
		} else {
			curlSnippet = string(curlOut)
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"board":        board,
			"raw_status":   rawStatus,
			"raw_body":     rawBody,
			"curl_err":     fmt.Sprint(curlErr),
			"curl_snippet": curlSnippet,
			"html_count":   len(arts),
			"html_err":     fmt.Sprint(err),
			"rss_count":    len(rssArts),
			"rss_err":      fmt.Sprint(rssErr),
		})
	})

	// Debug trigger endpoint to run checkBoard synchronously and inspect step-by-step
	mux.HandleFunc("/debug/trigger", func(w http.ResponseWriter, r *http.Request) {
		board := r.URL.Query().Get("b")
		if board == "" {
			board = "gossiping"
		}

		subs, subErr := db.GetBoardSubscribers(board)

		// 1. Fetch
		arts, crawlErr := pttCrawler.FetchArticles(board)
		usedRSS := false
		if crawlErr != nil {
			rssArts, rssErr := pttCrawler.FetchArticlesRSS(board)
			if rssErr == nil && len(rssArts) > 0 {
				arts = rssArts
				usedRSS = true
			}
		}

		type matchLog struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Matched  bool   `json:"matched"`
			Trigger  string `json:"trigger"`
			PushErr  string `json:"push_err"`
			SavedErr string `json:"saved_err"`
		}

		var logs []matchLog
		for _, art := range arts {
			m := matchLog{
				ID:    art.ID,
				Title: art.Title,
			}
			titleLower := strings.ToLower(art.Title)
			for _, sub := range subs {
				if sub.Type == "keyword" {
					kw := strings.ToLower(sub.Value)
					if strings.Contains(titleLower, kw) {
						m.Matched = true
						m.Trigger = sub.Value
						msg := fmt.Sprintf("【PTT 文章通知】\n看板：%s\n觸發：關鍵字【%s】\n標題：%s\n作者：%s\n連結：%s", art.Board, sub.Value, art.Title, art.Author, art.Link)
						pushErr := lineBot.PushAlert(sub.LineID, msg)
						if pushErr != nil {
							m.PushErr = fmt.Sprint(pushErr)
						}
					}
				}
			}
			saveErr := db.RecordArticleSeen(board, art.ID, art.Title, art.Author, art.PushSum)
			if saveErr != nil {
				m.SavedErr = fmt.Sprint(saveErr)
			}
			logs = append(logs, m)
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"board":      board,
			"subs_count": len(subs),
			"sub_err":    fmt.Sprint(subErr),
			"used_rss":   usedRSS,
			"art_count":  len(arts),
			"logs":       logs,
		})
	})

	// LINE Webhook
	mux.HandleFunc("/line/callback", lineBot.HandleWebhook)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	// 6. Graceful Shutdown Listener
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Server listening on port %s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	<-stopCh
	log.Println("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("PTT Alertor stopped gracefully")
}
