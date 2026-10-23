package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port                 string
	LineChannelSecret    string
	LineChannelAccessToken string
	DatabaseURL          string
	CheckIntervalSec     int
	CleanupIntervalHr    int
	AppURL               string
}

func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	checkInterval := 15
	if s := os.Getenv("CHECK_INTERVAL_SECONDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			checkInterval = v
		}
	}

	cleanupInterval := 24
	if s := os.Getenv("CLEANUP_INTERVAL_HOURS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			cleanupInterval = v
		}
	}

	return &Config{
		Port:                 port,
		LineChannelSecret:    os.Getenv("LINE_CHANNEL_SECRET"),
		LineChannelAccessToken: os.Getenv("LINE_CHANNEL_ACCESS_TOKEN"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		CheckIntervalSec:     checkInterval,
		CleanupIntervalHr:    cleanupInterval,
		AppURL:               os.Getenv("APP_URL"),
	}
}
