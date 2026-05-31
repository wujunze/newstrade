package model

import (
	"encoding/json"
	"log"
	"strconv"
	"time"
)

type WSSMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      interface{}      `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  *SubscribeResult `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

type SubscribeResult struct {
	Success bool `json:"success"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewsParams covers both news.update and news.ai_update payloads.
//
// news.update  → id, optional aiRating nested object
// news.ai_update → newsId, top-level score/grade/signal fields
//
// id/newsId may arrive as a JSON number or a quoted string, so they are
// parsed with FlexID which preserves the original textual form. This matches
// the VARCHAR primary key column and avoids int64 overflow / parse failures.
type NewsParams struct {
	ID         FlexID     `json:"id"`
	NewsID     FlexID     `json:"newsId"`
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
// Returns "" only when both id and newsId are empty/zero (caller should reject).
func (p *NewsParams) ArticleID() string {
	if id := p.ID.String(); id != "" {
		return id
	}
	return p.NewsID.String()
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

// FlexID parses an id that may be a JSON number or a quoted string.
// The zero value (empty string) means "absent".
type FlexID string

func (f *FlexID) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" || s == "" {
		*f = ""
		return nil
	}
	// Quoted string form: "12345" → 12345
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*f = FlexID(str)
		return nil
	}
	// Numeric form. Normalise to an integer string when it is an integer so a
	// number and its string twin map to the same primary key. Trim a trailing
	// ".0"-style fraction defensively if the source ever emits a float.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = FlexID(strconv.FormatInt(n, 10))
		return nil
	}
	*f = FlexID(s)
	return nil
}

// String returns the id text, or "" for the zero/"0" value.
func (f FlexID) String() string {
	s := string(f)
	if s == "0" {
		return ""
	}
	return s
}

// FlexTime parses ts that may be an int64 (Unix ms) or an ISO 8601 string.
type FlexTime struct {
	T *time.Time
}

func (f *FlexTime) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" || s == "" {
		f.T = nil
		return nil
	}

	var ms int64
	if err := json.Unmarshal(b, &ms); err == nil && ms > 0 {
		t := time.UnixMilli(ms)
		f.T = &t
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		if str == "" {
			f.T = nil
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if t, err := time.Parse(layout, str); err == nil {
				f.T = &t
				return nil
			}
		}
	}
	// Unparseable but non-empty: surface it instead of silently dropping.
	log.Printf("warning: could not parse ts %s", s)
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
