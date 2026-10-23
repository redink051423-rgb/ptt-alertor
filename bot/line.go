package bot

import (
	"fmt"
	"log"
	"net/http"

	"github.com/line/line-bot-sdk-go/v7/linebot"
	"github.com/ptt-alertor/ptt-alertor/storage"
)

type LineBot struct {
	client     *linebot.Client
	cmdHandler *CommandHandler
	db         *storage.DB
}

func NewLineBot(secret, token string, cmdHandler *CommandHandler, db *storage.DB) (*LineBot, error) {
	client, err := linebot.New(secret, token)
	if err != nil {
		return nil, fmt.Errorf("failed to init linebot: %w", err)
	}

	return &LineBot{
		client:     client,
		cmdHandler: cmdHandler,
		db:         db,
	}, nil
}

// HandleWebhook processes incoming webhook requests from LINE
func (b *LineBot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	events, err := b.client.ParseRequest(r)
	if err != nil {
		log.Printf("Line ParseRequest error: %v", err)
		if err == linebot.ErrInvalidSignature {
			w.WriteHeader(http.StatusBadRequest)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	for _, event := range events {
		lineID := b.extractSourceID(event.Source)
		if lineID == "" {
			continue
		}

		switch event.Type {
		case linebot.EventTypeFollow:
			log.Printf("User followed: %s", lineID)
			_ = b.db.UpsertUser(lineID)
			welcomeMsg := "歡迎使用 PTT Alertor！\n本服務支援即時追蹤 PTT 看板關鍵字、作者與推文數。\n\n請輸入「指令」查看設定方式，或輸入「新增 gossiping 台積電」立即開始追蹤！"
			_, _ = b.client.ReplyMessage(event.ReplyToken, linebot.NewTextMessage(welcomeMsg)).Do()

		case linebot.EventTypeUnfollow:
			log.Printf("User unfollowed: %s", lineID)
			_ = b.db.SetUserEnable(lineID, false)

		case linebot.EventTypeMessage:
			switch msg := event.Message.(type) {
			case *linebot.TextMessage:
				replyText := b.cmdHandler.Handle(lineID, msg.Text)
				if replyText != "" {
					if _, err := b.client.ReplyMessage(event.ReplyToken, linebot.NewTextMessage(replyText)).Do(); err != nil {
						log.Printf("Failed to reply to %s: %v", lineID, err)
					}
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (b *LineBot) extractSourceID(src *linebot.EventSource) string {
	if src == nil {
		return ""
	}
	if src.UserID != "" {
		return src.UserID
	}
	if src.GroupID != "" {
		return src.GroupID
	}
	if src.RoomID != "" {
		return src.RoomID
	}
	return ""
}

// PushAlert sends a notification message to a specific LINE user
func (b *LineBot) PushAlert(lineID, message string) error {
	_, err := b.client.PushMessage(lineID, linebot.NewTextMessage(message)).Do()
	if err != nil {
		log.Printf("PushMessage to %s error: %v", lineID, err)
		return err
	}
	return nil
}

// MulticastAlert sends a notification message to multiple LINE users in batches
func (b *LineBot) MulticastAlert(lineIDs []string, message string) error {
	if len(lineIDs) == 0 {
		return nil
	}
	if len(lineIDs) == 1 {
		return b.PushAlert(lineIDs[0], message)
	}

	// LINE Multicast supports up to 500 recipients per call
	batchSize := 450
	for i := 0; i < len(lineIDs); i += batchSize {
		end := i + batchSize
		if end > len(lineIDs) {
			end = len(lineIDs)
		}
		batch := lineIDs[i:end]
		if _, err := b.client.Multicast(batch, linebot.NewTextMessage(message)).Do(); err != nil {
			log.Printf("Multicast error: %v", err)
		}
	}
	return nil
}
