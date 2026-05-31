package service

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
	"newstrade/internal/model"
)

var db *sql.DB

func InitDB() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}

	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if err = db.Ping(); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(3)
	db.SetConnMaxLifetime(5 * time.Minute) // Bug4 fix: prevent stale connections

	log.Println("database connected")
	return nil
}

func MigrateDB() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS news_articles (
			id          VARCHAR(255) PRIMARY KEY,
			text        TEXT         NOT NULL DEFAULT '',
			news_type   VARCHAR(255) NOT NULL DEFAULT '',
			engine_type VARCHAR(100) NOT NULL DEFAULT '',
			link        TEXT         NOT NULL DEFAULT '',
			ai_score    INTEGER,
			ai_grade    VARCHAR(20),
			ai_signal   VARCHAR(50),
			ts          TIMESTAMPTZ,
			raw_json    JSONB        NOT NULL DEFAULT '{}',
			created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_news_articles_news_type   ON news_articles (news_type);
		CREATE INDEX IF NOT EXISTS idx_news_articles_engine_type ON news_articles (engine_type);
		CREATE INDEX IF NOT EXISTS idx_news_articles_ts          ON news_articles (ts DESC NULLS LAST);
		CREATE INDEX IF NOT EXISTS idx_news_articles_ai_signal   ON news_articles (ai_signal);
		CREATE INDEX IF NOT EXISTS idx_news_articles_created_at  ON news_articles (created_at DESC);

		CREATE TABLE IF NOT EXISTS news_coins (
			id          BIGSERIAL    PRIMARY KEY,
			news_id     VARCHAR(255) NOT NULL REFERENCES news_articles (id) ON DELETE CASCADE,
			symbol      VARCHAR(255) NOT NULL DEFAULT '',
			market_type VARCHAR(50),
			match_field VARCHAR(50),
			score       INTEGER,
			signal      VARCHAR(50),
			grade       VARCHAR(20),
			created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_news_coins_news_id ON news_coins (news_id);
		CREATE INDEX IF NOT EXISTS idx_news_coins_symbol  ON news_coins (symbol);
		CREATE INDEX IF NOT EXISTS idx_news_coins_signal  ON news_coins (signal);
	`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Println("database migrated")
	return nil
}

func UpsertArticle(params *model.NewsParams, rawParams json.RawMessage) error {
	if params.ArticleID() == "0" {
		return fmt.Errorf("invalid article id: both id and newsId are 0")
	}

	raw, err := marshalParams(params, rawParams)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	aiScore := params.EffectiveAIScore()
	aiGrade := params.EffectiveAIGrade()
	aiSignal := params.EffectiveAISignal()

	articleID := params.ArticleID()
	// Bug1 fix: use COALESCE so ai_score/grade/signal set by news.ai_update
	// are never overwritten with NULL by a later news.update for the same id.
	_, err = tx.Exec(`
		INSERT INTO news_articles (id, text, news_type, engine_type, link, ai_score, ai_grade, ai_signal, ts, raw_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
			text        = EXCLUDED.text,
			news_type   = EXCLUDED.news_type,
			engine_type = EXCLUDED.engine_type,
			link        = EXCLUDED.link,
			ai_score    = COALESCE(EXCLUDED.ai_score,  news_articles.ai_score),
			ai_grade    = COALESCE(EXCLUDED.ai_grade,  news_articles.ai_grade),
			ai_signal   = COALESCE(EXCLUDED.ai_signal, news_articles.ai_signal),
			ts          = COALESCE(EXCLUDED.ts,        news_articles.ts),
			raw_json    = EXCLUDED.raw_json
	`, articleID, params.Text, params.NewsType, params.EngineType, params.Link,
		aiScore, aiGrade, aiSignal, params.Ts.T, raw)
	if err != nil {
		return fmt.Errorf("upsert article: %w", err)
	}

	// Replace coins for this article.
	_, err = tx.Exec(`DELETE FROM news_coins WHERE news_id = $1`, articleID)
	if err != nil {
		return fmt.Errorf("delete old coins: %w", err)
	}

	for _, c := range params.Coins {
		matchField := nullableString(c.Match)
		signal := nullableString(c.Signal)
		grade := nullableString(c.Grade)
		_, err = tx.Exec(`
			INSERT INTO news_coins (news_id, symbol, market_type, match_field, score, signal, grade)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, articleID, c.Symbol, c.MarketType, matchField, c.Score, signal, grade)
		if err != nil {
			return fmt.Errorf("insert coin %s: %w", c.Symbol, err)
		}
	}

	return tx.Commit()
}

func marshalParams(_ *model.NewsParams, rawParams json.RawMessage) ([]byte, error) {
	if len(rawParams) > 0 {
		return rawParams, nil
	}
	return nil, fmt.Errorf("empty raw params")
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
