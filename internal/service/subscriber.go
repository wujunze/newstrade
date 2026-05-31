package service

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"newstrade/internal/model"
)

const (
	wssBase      = "wss://ai.6551.io/open/news_wss"
	pingInterval = 30 * time.Second
	readTimeout  = pingInterval * 3 // Bug2 fix: read deadline = 3x ping interval
	maxBackoff   = 60 * time.Second
	stableAfter  = 30 * time.Second // Bug3 fix: reset backoff if connection lasted this long
)

func RunSubscriber() {
	token := os.Getenv("WSS_TOKEN")
	if token == "" {
		log.Fatal("WSS_TOKEN is not set")
	}
	url := fmt.Sprintf("%s?token=%s", wssBase, token)

	attempt := 0
	for {
		start := time.Now()
		err := connect(url)
		// Bug3 fix: if connection was stable for stableAfter, reset backoff counter.
		if time.Since(start) >= stableAfter {
			attempt = 0
		}
		attempt++
		wait := backoff(attempt)
		log.Printf("subscriber disconnected (%v) — retrying in %s (attempt %d)", err, wait, attempt)
		time.Sleep(wait)
	}
}

func connect(url string) error {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	log.Println("WebSocket connected")

	if err := subscribe(conn); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	// Bug2 fix: set initial read deadline and reset it on every message.
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	done := make(chan error, 1)

	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			// Bug2 fix: reset deadline on any received message, not only pong.
			conn.SetReadDeadline(time.Now().Add(readTimeout))
			if err := handleMessage(msg); err != nil {
				log.Printf("handle message error: %v", err)
			}
		}
	}()

	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
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
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, b)
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

func backoff(attempt int) time.Duration {
	d := time.Duration(math.Pow(2, float64(attempt))) * time.Second
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}
