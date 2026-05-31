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
