package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"newstrade/internal/model"
)

const (
	wssBase         = "wss://ai.6551.io/open/news_wss"
	pingInterval    = 30 * time.Second
	readTimeout     = pingInterval * 3 // read deadline = 3x ping interval
	writeTimeout    = 10 * time.Second
	maxBackoffExp   = 6 // clamp 2^exp so backoff never overflows
	maxBackoff      = 60 * time.Second
	stableAfter     = 30 * time.Second // reset backoff if a connection lasted this long
	authFailBackoff = 60 * time.Second // long wait when the server rejects our subscription
	writeQueueSize  = 1024             // buffered DB-write queue
)

// healthState tracks the subscriber's liveness for the /health endpoint.
type healthState struct {
	connected     atomic.Bool
	lastMessageNs atomic.Int64 // UnixNano of last message received
	lastConnectNs atomic.Int64 // UnixNano of last successful connect
}

var health healthState

// Healthy reports whether the subscriber is currently connected and has shown
// activity (a message or a fresh connection) within maxIdle.
func Healthy(maxIdle time.Duration) bool {
	if !health.connected.Load() {
		return false
	}
	last := health.lastMessageNs.Load()
	if c := health.lastConnectNs.Load(); c > last {
		last = c
	}
	if last == 0 {
		return false
	}
	return time.Since(time.Unix(0, last)) <= maxIdle
}

// RunSubscriber connects to the news WSS feed and persists every message until
// ctx is cancelled. It blocks until ctx is done.
func RunSubscriber(ctx context.Context, token string) {
	endpoint := fmt.Sprintf("%s?token=%s", wssBase, url.QueryEscape(token))
	// Token is also sent as a header; some gateways prefer it there and it keeps
	// the secret out of any logging that echoes only the URL path.
	header := http.Header{"Authorization": {"Bearer " + token}}

	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := connect(ctx, endpoint, header)
		if ctx.Err() != nil {
			return
		}

		// A connection that stayed up for stableAfter is treated as healthy:
		// reset the backoff and reconnect immediately.
		if time.Since(start) >= stableAfter {
			attempt = 0
			log.Printf("subscriber disconnected (%v) — reconnecting immediately", err)
			continue
		}

		var wait time.Duration
		var authErr *authError
		if errors.As(err, &authErr) {
			wait = authFailBackoff
			log.Printf("subscription rejected by server (%v) — retrying in %s", err, wait)
		} else {
			attempt++
			wait = backoff(attempt)
			log.Printf("subscriber disconnected (%v) — retrying in %s (attempt %d)", err, wait, attempt)
		}
		if !sleepCtx(ctx, wait) {
			return
		}
	}
}

func connect(ctx context.Context, endpoint string, header http.Header) (retErr error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, header)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	log.Println("WebSocket connected")

	if err := subscribe(conn); err != nil {
		conn.Close()
		return fmt.Errorf("subscribe: %w", err)
	}

	health.connected.Store(true)
	health.lastConnectNs.Store(time.Now().UnixNano())

	conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})

	// Decouple reading from persistence: the read loop only enqueues, a worker
	// writes to the DB. This prevents a slow DB from stalling reads (and the
	// read deadline) and provides backpressure via a bounded queue.
	queue := make(chan []byte, writeQueueSize)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range queue {
			if err := handleMessage(msg); err != nil {
				log.Printf("handle message error: %v", err)
			}
		}
	}()

	// Cleanup order matters: closing the connection unblocks ReadMessage, which
	// lets the read goroutine close(queue), which lets the worker drain and exit
	// so wg.Wait() can return. Doing this in one defer avoids a deadlock.
	defer func() {
		conn.Close()
		wg.Wait()
		health.connected.Store(false)
	}()

	done := make(chan error, 1)
	go func() {
		defer close(queue)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			conn.SetReadDeadline(time.Now().Add(readTimeout))
			health.lastMessageNs.Store(time.Now().UnixNano())

			// Detect a rejected subscription so the caller can back off hard
			// instead of hammering the server with an invalid token.
			if ae := authRejection(msg); ae != nil {
				done <- ae
				return
			}

			select {
			case queue <- msg:
			default:
				log.Printf("warning: write queue full, dropping message")
			}
		}
	}()

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			return err
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return fmt.Errorf("ping: %w", err)
			}
		}
	}
}

func subscribe(conn *websocket.Conn) error {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "news.subscribe",
		"params":  map[string]interface{}{},
	}
	b, _ := json.Marshal(msg)
	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return conn.WriteMessage(websocket.TextMessage, b)
}

// authError marks a subscription/auth rejection that should not be retried with
// the normal fast exponential backoff.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

// authRejection inspects a server frame for an explicit subscription failure or
// JSON-RPC error on our subscribe request (id == 1).
func authRejection(raw []byte) *authError {
	var m model.WSSMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	if m.Error != nil {
		return &authError{msg: fmt.Sprintf("rpc error %d: %s", m.Error.Code, m.Error.Message)}
	}
	if m.Result != nil && !m.Result.Success {
		return &authError{msg: "subscribe result success=false"}
	}
	return nil
}

func handleMessage(raw []byte) error {
	var msg model.WSSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	switch msg.Method {
	case "news.update", "news.ai_update":
		if len(msg.Params) == 0 {
			return nil
		}
		var params model.NewsParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return fmt.Errorf("unmarshal params: %w", err)
		}
		if err := UpsertArticle(&params, msg.Params); err != nil {
			return fmt.Errorf("upsert %s: %w", params.ArticleID(), err)
		}
		log.Printf("saved [%s] id=%s %s/%s", msg.Method, params.ArticleID(), params.EngineType, params.NewsType)
	}
	return nil
}

// backoff returns 2^attempt seconds, clamped to maxBackoff. The exponent is
// clamped first so the intermediate value can never overflow time.Duration.
func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > maxBackoffExp {
		attempt = maxBackoffExp
	}
	d := time.Duration(1<<uint(attempt)) * time.Second
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// sleepCtx sleeps for d but returns false immediately if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
