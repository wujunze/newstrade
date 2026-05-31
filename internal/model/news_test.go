package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFlexID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // expected ArticleID-style String() output
	}{
		{"number", `12345`, "12345"},
		{"string", `"67890"`, "67890"},
		{"zero number", `0`, ""},
		{"zero string", `"0"`, ""},
		{"null", `null`, ""},
		{"empty string", `""`, ""},
		{"big beyond int64", `"123456789012345678901234"`, "123456789012345678901234"},
		{"non numeric string", `"abc-123"`, "abc-123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var id FlexID
			if err := json.Unmarshal([]byte(c.in), &id); err != nil {
				t.Fatalf("unmarshal %q: %v", c.in, err)
			}
			if got := id.String(); got != c.want {
				t.Fatalf("String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestArticleID(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"id only", `{"id":111}`, "111"},
		{"newsId only", `{"newsId":222}`, "222"},
		{"id preferred over newsId", `{"id":111,"newsId":222}`, "111"},
		{"both zero", `{"id":0,"newsId":0}`, ""},
		{"id string newsId number", `{"id":"abc","newsId":222}`, "abc"},
		{"id zero falls through to newsId", `{"id":0,"newsId":222}`, "222"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p NewsParams
			if err := json.Unmarshal([]byte(c.json), &p); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := p.ArticleID(); got != c.want {
				t.Fatalf("ArticleID() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFlexTime(t *testing.T) {
	wantMs := time.UnixMilli(1700000000000)
	cases := []struct {
		name    string
		in      string
		wantNil bool
		want    time.Time
	}{
		{"unix ms", `1700000000000`, false, wantMs},
		{"rfc3339", `"2023-11-14T22:13:20Z"`, false, time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC)},
		{"rfc3339nano", `"2023-11-14T22:13:20.5Z"`, false, time.Date(2023, 11, 14, 22, 13, 20, 5e8, time.UTC)},
		{"null", `null`, true, time.Time{}},
		{"empty string", `""`, true, time.Time{}},
		{"zero", `0`, true, time.Time{}},
		{"garbage", `"not-a-time"`, true, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ft FlexTime
			if err := json.Unmarshal([]byte(c.in), &ft); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if c.wantNil {
				if ft.T != nil {
					t.Fatalf("expected nil time, got %v", *ft.T)
				}
				return
			}
			if ft.T == nil {
				t.Fatalf("expected %v, got nil", c.want)
			}
			if !ft.T.Equal(c.want) {
				t.Fatalf("got %v, want %v", *ft.T, c.want)
			}
		})
	}
}

func TestEffectiveAIFields(t *testing.T) {
	score := 7
	t.Run("top-level wins", func(t *testing.T) {
		p := NewsParams{
			TopScore:  &score,
			TopGrade:  "A",
			TopSignal: "buy",
			AIRating:  &AIRating{Score: 1, Grade: "C", Signal: "sell"},
		}
		if s := p.EffectiveAIScore(); s == nil || *s != 7 {
			t.Fatalf("score = %v, want 7", s)
		}
		if g := p.EffectiveAIGrade(); g == nil || *g != "A" {
			t.Fatalf("grade = %v, want A", g)
		}
		if sig := p.EffectiveAISignal(); sig == nil || *sig != "buy" {
			t.Fatalf("signal = %v, want buy", sig)
		}
	})
	t.Run("nested fallback", func(t *testing.T) {
		p := NewsParams{AIRating: &AIRating{Score: 3, Grade: "B", Signal: "hold"}}
		if s := p.EffectiveAIScore(); s == nil || *s != 3 {
			t.Fatalf("score = %v, want 3", s)
		}
		if g := p.EffectiveAIGrade(); g == nil || *g != "B" {
			t.Fatalf("grade = %v, want B", g)
		}
		if sig := p.EffectiveAISignal(); sig == nil || *sig != "hold" {
			t.Fatalf("signal = %v, want hold", sig)
		}
	})
	t.Run("absent", func(t *testing.T) {
		p := NewsParams{}
		if p.EffectiveAIScore() != nil || p.EffectiveAIGrade() != nil || p.EffectiveAISignal() != nil {
			t.Fatalf("expected all nil for empty params")
		}
	})
}
