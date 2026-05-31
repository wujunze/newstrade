package model

import (
	"encoding/json"
	"strconv"
	"time"
)

type WSSMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  *SubscribeResult `json:"result,omitempty"`
}

type SubscribeResult struct {
	Success bool `json:"success"`
}

// NewsParams covers both news.update and news.ai_update payloads.
//
// news.update  → id (int64), optional aiRating nested object
// news.ai_update → newsId (int64), top-level score/grade/signal fields
type NewsParams struct {
	ID         int64      `json:"id"`
	NewsID     int64      `json:"newsId"`
	Text       string     `json:"text"`
	NewsType   string     `json:"newsType"`
	EngineType string     `json:"engineType"`
	Link       string     `json:"link"`
	Coins      []CoinInfo `json:"coins"`
	Ts         FlexTime   `json:"ts"`

	// Nested AI rating (news.update style, rarely populated)
	AIRating *AIRating `json:"aiRating,omitempty"`

	// Top-level AI fields (news.ai_update actual format observed in the wild)
	TopScore  *int   `json:"score,omitempty"`
	TopGrade  string `json:"grade,omitempty"`
	TopSignal string `json:"signal,omitempty"`
}

// ArticleID returns the article ID as a string.
// Returns "0" only when both id and newsId are zero (caller should reject).
func (p *NewsParams) ArticleID() string {
	if p.ID != 0 {
		return strconv.FormatInt(p.ID, 10)
	}
	if p.NewsID != 0 {
		return strconv.FormatInt(p.NewsID, 10)
	}
	return "0"
}

// EffectiveAIScore returns the AI score from whichever source has it.
func (p *NewsParams) EffectiveAIScore() *int {
	if p.TopScore != nil {
		return p.TopScore
	}
	if p.AIRating != nil {
		return &p.AIRating.Score
	}
	return nil
}

// EffectiveAIGrade returns the AI grade from whichever source has it.
func (p *NewsParams) EffectiveAIGrade() *string {
	if p.TopGrade != "" {
		return &p.TopGrade
	}
	if p.AIRating != nil && p.AIRating.Grade != "" {
		return &p.AIRating.Grade
	}
	return nil
}

// EffectiveAISignal returns the AI signal from whichever source has it.
func (p *NewsParams) EffectiveAISignal() *string {
	if p.TopSignal != "" {
		return &p.TopSignal
	}
	if p.AIRating != nil && p.AIRating.Signal != "" {
		return &p.AIRating.Signal
	}
	return nil
}

// FlexTime parses ts that may be an int64 (Unix ms) or an ISO 8601 string.
type FlexTime struct {
	T *time.Time
}

func (f *FlexTime) UnmarshalJSON(b []byte) error {
	var ms int64
	if err := json.Unmarshal(b, &ms); err == nil && ms > 0 {
		t := time.UnixMilli(ms)
		f.T = &t
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil && s != "" {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if t, err := time.Parse(layout, s); err == nil {
				f.T = &t
				return nil
			}
		}
	}
	f.T = nil
	return nil
}

type CoinInfo struct {
	Symbol     string `json:"symbol"`
	MarketType string `json:"market_type"`
	Match      string `json:"match,omitempty"`
	Score      *int   `json:"score,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Grade      string `json:"grade,omitempty"`
}

type AIRating struct {
	Score  int    `json:"score"`
	Grade  string `json:"grade,omitempty"`
	Signal string `json:"signal,omitempty"`
}
