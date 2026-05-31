package service

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{-5, 2 * time.Second}, // treated as attempt 1
		{0, 2 * time.Second},  // treated as attempt 1
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
		{6, maxBackoff},  // 64s clamped to 60s
		{7, maxBackoff},  // exponent clamped
		{34, maxBackoff}, // the old overflow point — must stay clamped
		{1000, maxBackoff},
	}
	for _, c := range cases {
		got := backoff(c.attempt)
		if got != c.want {
			t.Errorf("backoff(%d) = %s, want %s", c.attempt, got, c.want)
		}
		if got <= 0 || got > maxBackoff {
			t.Errorf("backoff(%d) = %s out of (0, %s] range", c.attempt, got, maxBackoff)
		}
	}
}

func TestAuthRejection(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		reject bool
	}{
		{"rpc error", `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"invalid token"}}`, true},
		{"success false", `{"jsonrpc":"2.0","id":1,"result":{"success":false}}`, true},
		{"success true", `{"jsonrpc":"2.0","id":1,"result":{"success":true}}`, false},
		{"news update frame", `{"jsonrpc":"2.0","method":"news.update","params":{"id":1}}`, false},
		{"garbage", `not json`, false},
		{"empty object", `{}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := authRejection([]byte(c.raw))
			if c.reject && got == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if !c.reject && got != nil {
				t.Fatalf("expected no rejection, got %v", got)
			}
		})
	}
}

func TestHealthyReflectsState(t *testing.T) {
	// Save and restore global state to avoid cross-test contamination.
	defer func() {
		health.connected.Store(false)
		health.lastMessageNs.Store(0)
		health.lastConnectNs.Store(0)
	}()

	health.connected.Store(false)
	if Healthy(time.Minute) {
		t.Fatal("disconnected should be unhealthy")
	}

	health.connected.Store(true)
	health.lastConnectNs.Store(time.Now().UnixNano())
	health.lastMessageNs.Store(0)
	if !Healthy(time.Minute) {
		t.Fatal("fresh connect should be healthy")
	}

	health.lastConnectNs.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	if Healthy(time.Minute) {
		t.Fatal("stale (2m idle) should be unhealthy within 1m window")
	}
}
