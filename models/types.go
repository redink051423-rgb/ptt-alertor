package models

import "time"

// SubscriptionType represents the alert rule type
type SubscriptionType string

const (
	SubTypeKeyword        SubscriptionType = "keyword"
	SubTypeAuthor         SubscriptionType = "author"
	SubTypePushSum        SubscriptionType = "pushsum"
	SubTypeExcludeKeyword SubscriptionType = "exclude"
)

// User represents a registered LINE user
type User struct {
	LineID    string    `json:"line_id"`
	Enable    bool      `json:"enable"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Subscription represents an alert rule
type Subscription struct {
	ID        int64            `json:"id"`
	LineID    string           `json:"line_id"`
	Board     string           `json:"board"`
	Type      SubscriptionType `json:"type"`
	Value     string           `json:"value"`
	CreatedAt time.Time        `json:"created_at"`
}

// Article represents a scraped PTT article
type Article struct {
	ID      string `json:"id"`       // e.g. M.1792757650.A.BB3
	Board   string `json:"board"`    // e.g. Gossiping
	Title   string `json:"title"`    // e.g. [問卦] 今天天氣真好
	Author  string `json:"author"`   // e.g. wayjust1
	Link    string `json:"link"`     // e.g. https://www.ptt.cc/bbs/Gossiping/M.1792757650.A.BB3.html
	Date    string `json:"date"`     // e.g. 10/23
	PushSum int    `json:"push_sum"` // e.g. 100 for 爆, -100 for XX, or integer
}
