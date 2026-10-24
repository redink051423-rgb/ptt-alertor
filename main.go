package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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
		arts, err := pttCrawler.FetchArticles(board)
		rssArts, rssErr := pttCrawler.FetchArticlesRSS(board)
		exists := pttCrawler.CheckBoardExist(board)

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"board":      board,
			"exists":     exists,
			"html_count": len(arts),
			"html_err":   fmt.Sprint(err),
			"rss_count":  len(rssArts),
			"rss_err":    fmt.Sprint(rssErr),
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
