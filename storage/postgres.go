package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/ptt-alertor/ptt-alertor/models"
)

type DB struct {
	db *sql.DB
}

func NewDB(connStr string) (*DB, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &DB{db: db}
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	return s, nil
}

func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		line_id VARCHAR(64) PRIMARY KEY,
		enable BOOLEAN NOT NULL DEFAULT TRUE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS subscriptions (
		id BIGSERIAL PRIMARY KEY,
		line_id VARCHAR(64) NOT NULL REFERENCES users(line_id) ON DELETE CASCADE,
		board VARCHAR(64) NOT NULL,
		type VARCHAR(32) NOT NULL,
		value TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		CONSTRAINT uq_user_board_type_val UNIQUE(line_id, board, type, value)
	);

	CREATE INDEX IF NOT EXISTS idx_subs_board_type ON subscriptions (board, type);
	CREATE INDEX IF NOT EXISTS idx_subs_line_id ON subscriptions (line_id);

	CREATE TABLE IF NOT EXISTS articles_seen (
		board VARCHAR(64) NOT NULL,
		article_id VARCHAR(64) NOT NULL,
		title TEXT NOT NULL,
		author VARCHAR(64) NOT NULL,
		push_sum INT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (board, article_id)
	);

	CREATE INDEX IF NOT EXISTS idx_articles_created_at ON articles_seen (created_at);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *DB) UpsertUser(lineID string) error {
	query := `
	INSERT INTO users (line_id, enable, updated_at)
	VALUES ($1, true, NOW())
	ON CONFLICT (line_id) DO UPDATE SET enable = true, updated_at = NOW();
	`
	_, err := s.db.Exec(query, lineID)
	return err
}

func (s *DB) SetUserEnable(lineID string, enable bool) error {
	query := `UPDATE users SET enable = $1, updated_at = NOW() WHERE line_id = $2;`
	_, err := s.db.Exec(query, enable, lineID)
	return err
}

func (s *DB) AddSubscription(lineID, board string, subType models.SubscriptionType, value string) error {
	if err := s.UpsertUser(lineID); err != nil {
		return err
	}
	board = strings.ToLower(strings.TrimSpace(board))
	value = strings.TrimSpace(value)

	query := `
	INSERT INTO subscriptions (line_id, board, type, value)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (line_id, board, type, value) DO NOTHING;
	`
	_, err := s.db.Exec(query, lineID, board, string(subType), value)
	return err
}

func (s *DB) RemoveSubscription(lineID, board string, subType models.SubscriptionType, value string) (int64, error) {
	board = strings.ToLower(strings.TrimSpace(board))
	value = strings.TrimSpace(value)

	var res sql.Result
	var err error
	if value == "*" || value == "" {
		query := `DELETE FROM subscriptions WHERE line_id = $1 AND board = $2 AND type = $3;`
		res, err = s.db.Exec(query, lineID, board, string(subType))
	} else {
		query := `DELETE FROM subscriptions WHERE line_id = $1 AND board = $2 AND type = $3 AND value = $4;`
		res, err = s.db.Exec(query, lineID, board, string(subType), value)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *DB) RemoveAllSubscriptions(lineID string) error {
	query := `DELETE FROM subscriptions WHERE line_id = $1;`
	_, err := s.db.Exec(query, lineID)
	return err
}

func (s *DB) ListUserSubscriptions(lineID string) ([]models.Subscription, error) {
	query := `
	SELECT id, line_id, board, type, value, created_at
	FROM subscriptions
	WHERE line_id = $1
	ORDER BY board ASC, type ASC, value ASC;
	`
	rows, err := s.db.Query(query, lineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []models.Subscription
	for rows.Next() {
		var sub models.Subscription
		var t string
		if err := rows.Scan(&sub.ID, &sub.LineID, &sub.Board, &t, &sub.Value, &sub.CreatedAt); err != nil {
			return nil, err
		}
		sub.Type = models.SubscriptionType(t)
		subs = append(subs, sub)
	}
	return subs, nil
}

func (s *DB) GetDistinctSubscribedBoards() ([]string, error) {
	query := `
	SELECT DISTINCT s.board
	FROM subscriptions s
	JOIN users u ON s.line_id = u.line_id
	WHERE u.enable = true
	ORDER BY s.board ASC;
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var boards []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, nil
}

func (s *DB) GetBoardSubscribers(board string) ([]models.Subscription, error) {
	board = strings.ToLower(strings.TrimSpace(board))
	query := `
	SELECT s.id, s.line_id, s.board, s.type, s.value, s.created_at
	FROM subscriptions s
	JOIN users u ON s.line_id = u.line_id
	WHERE s.board = $1 AND u.enable = true;
	`
	rows, err := s.db.Query(query, board)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []models.Subscription
	for rows.Next() {
		var sub models.Subscription
		var t string
		if err := rows.Scan(&sub.ID, &sub.LineID, &sub.Board, &t, &sub.Value, &sub.CreatedAt); err != nil {
			return nil, err
		}
		sub.Type = models.SubscriptionType(t)
		subs = append(subs, sub)
	}
	return subs, nil
}

func (s *DB) IsArticleSeen(board, articleID string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM articles_seen WHERE board = $1 AND article_id = $2);`
	var exists bool
	err := s.db.QueryRow(query, board, articleID).Scan(&exists)
	return exists, err
}

func (s *DB) GetArticlePushSum(board, articleID string) (int, bool, error) {
	query := `SELECT push_sum FROM articles_seen WHERE board = $1 AND article_id = $2;`
	var pushSum int
	err := s.db.QueryRow(query, board, articleID).Scan(&pushSum)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return pushSum, true, nil
}

func (s *DB) RecordArticleSeen(board, articleID, title, author string, pushSum int) error {
	query := `
	INSERT INTO articles_seen (board, article_id, title, author, push_sum, created_at)
	VALUES ($1, $2, $3, $4, $5, NOW())
	ON CONFLICT (board, article_id) DO UPDATE
	SET push_sum = $5;
	`
	_, err := s.db.Exec(query, board, articleID, title, author, pushSum)
	return err
}

func (s *DB) CleanOldArticles(days int) error {
	query := fmt.Sprintf(`DELETE FROM articles_seen WHERE created_at < NOW() - INTERVAL '%d days';`, days)
	_, err := s.db.Exec(query)
	return err
}
