package service

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"newstrade/internal/model"
)

// setupTestDB connects using DATABASE_URL and runs the migration. The whole
// store test suite is skipped when DATABASE_URL is not set so `go test ./...`
// stays green on machines without a database.
func setupTestDB(t *testing.T) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping store integration tests")
	}
	if err := InitDB(); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	if err := MigrateDB(); err != nil {
		t.Fatalf("MigrateDB: %v", err)
	}
}

func cleanup(t *testing.T, id string) {
	t.Helper()
	if db == nil {
		return
	}
	if _, err := db.Exec(`DELETE FROM news_articles WHERE id = $1`, id); err != nil {
		t.Logf("cleanup %s: %v", id, err)
	}
}

// upsertJSON unmarshals a raw params payload and upserts it the same way the
// subscriber does.
func upsertJSON(t *testing.T, rawParams string) {
	t.Helper()
	var p model.NewsParams
	raw := json.RawMessage(rawParams)
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if err := UpsertArticle(&p, raw); err != nil {
		t.Fatalf("UpsertArticle: %v", err)
	}
}

func TestUpsertPreservesFieldsAcrossMessages(t *testing.T) {
	setupTestDB(t)
	const id = "999000111" // unlikely to collide with real data
	cleanup(t, id)
	defer cleanup(t, id)

	// 1) news.update: full content + coins, no AI fields.
	upsertJSON(t, `{
		"id": 999000111,
		"text": "Bitcoin breaks resistance",
		"newsType": "flash",
		"engineType": "twitter",
		"link": "https://example.com/a",
		"ts": 1700000000000,
		"coins": [{"symbol":"BTC","market_type":"spot","score":80,"signal":"buy","grade":"A"}]
	}`)

	// 2) news.ai_update: same article via newsId, AI fields, NO text, NO coins.
	//    Must not clobber text/coins set above; must add AI fields.
	upsertJSON(t, `{
		"newsId": 999000111,
		"score": 90,
		"grade": "S",
		"signal": "strong_buy"
	}`)

	var (
		text              string
		aiScore           sql.NullInt64
		aiGrade, aiSignal sql.NullString
		coinCount         int
		raw               []byte
	)
	row := db.QueryRow(`SELECT text, ai_score, ai_grade, ai_signal, raw_json FROM news_articles WHERE id = $1`, id)
	if err := row.Scan(&text, &aiScore, &aiGrade, &aiSignal, &raw); err != nil {
		t.Fatalf("scan article: %v", err)
	}
	if text != "Bitcoin breaks resistance" {
		t.Errorf("text was clobbered: %q", text)
	}
	if !aiScore.Valid || aiScore.Int64 != 90 {
		t.Errorf("ai_score = %v, want 90", aiScore)
	}
	if !aiGrade.Valid || aiGrade.String != "S" {
		t.Errorf("ai_grade = %v, want S", aiGrade)
	}
	if !aiSignal.Valid || aiSignal.String != "strong_buy" {
		t.Errorf("ai_signal = %v, want strong_buy", aiSignal)
	}

	if err := db.QueryRow(`SELECT count(*) FROM news_coins WHERE news_id = $1`, id).Scan(&coinCount); err != nil {
		t.Fatalf("count coins: %v", err)
	}
	if coinCount != 1 {
		t.Errorf("coins wiped by ai_update: count = %d, want 1", coinCount)
	}

	// raw_json must contain merged keys from both messages.
	var merged map[string]any
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("raw_json not valid json: %v", err)
	}
	if _, ok := merged["text"]; !ok {
		t.Errorf("raw_json lost news.update keys: %v", merged)
	}
	if _, ok := merged["score"]; !ok {
		t.Errorf("raw_json missing ai_update keys: %v", merged)
	}
}

func TestUpsertRejectsEmptyID(t *testing.T) {
	setupTestDB(t)
	var p model.NewsParams // both ids zero
	err := UpsertArticle(&p, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for empty id, got nil")
	}
}

func TestUpsertOrderIndependentAIThenUpdate(t *testing.T) {
	setupTestDB(t)
	const id = "999000222"
	cleanup(t, id)
	defer cleanup(t, id)

	// AI update arrives first.
	upsertJSON(t, `{"newsId":999000222,"score":50,"grade":"B","signal":"hold"}`)
	// Then the content update (no AI fields) — must not erase AI fields.
	upsertJSON(t, `{"id":999000222,"text":"late content","newsType":"flash","engineType":"rss","link":"x"}`)

	var (
		text    string
		aiScore sql.NullInt64
	)
	row := db.QueryRow(`SELECT text, ai_score FROM news_articles WHERE id = $1`, id)
	if err := row.Scan(&text, &aiScore); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if text != "late content" {
		t.Errorf("text = %q, want 'late content'", text)
	}
	if !aiScore.Valid || aiScore.Int64 != 50 {
		t.Errorf("ai_score = %v, want 50 (preserved)", aiScore)
	}
}
