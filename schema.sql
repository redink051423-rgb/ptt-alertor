-- PTT Alertor PostgreSQL Schema (Supabase compatible)

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
    type VARCHAR(32) NOT NULL, -- 'keyword', 'author', 'pushsum'
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
