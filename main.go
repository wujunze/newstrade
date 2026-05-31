package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	fs "io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"newstrade/internal/service"
)

//go:embed web
var webFS embed.FS

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	token := os.Getenv("WSS_TOKEN")
	if token == "" {
		log.Fatal("WSS_TOKEN is not set")
	}

	if err := service.InitDB(); err != nil {
		log.Fatalf("init db: %v", err)
	}
	defer service.CloseDB()

	if err := service.MigrateDB(); err != nil {
		log.Fatalf("migrate db: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		service.RunSubscriber(ctx, token)
	}()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/api/news", newsHandler)
	mux.HandleFunc("/api/news/filters", filtersHandler)

	// Serve embedded frontend — strip "web/" prefix so /index.html works.
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(webSub)))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		log.Println("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}()

	log.Printf("HTTP server on :%s", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http server: %v", err)
	}

	wg.Wait()
	log.Println("shutdown complete")
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	subConnected := service.Healthy(5 * time.Minute)

	if err := service.PingDB(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "db: down (%v)\nsubscriber: %v\n", err, subConnected)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok\ndb: up\nsubscriber: %v\n", subConnected)
}

func newsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	params := service.ListParams{
		Page:       queryInt(q.Get("page"), 1),
		PageSize:   queryInt(q.Get("page_size"), 24),
		Signal:     q.Get("signal"),
		NewsType:   q.Get("news_type"),
		EngineType: q.Get("engine"),
		Symbol:     q.Get("symbol"),
		Search:     q.Get("search"),
	}

	result, err := service.ListArticles(params)
	if err != nil {
		log.Printf("list articles: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, result)
}

func filtersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	signals, newsTypes, engineTypes, err := service.FilterOptions()
	if err != nil {
		log.Printf("filter options: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"signals":      orEmpty(signals),
		"news_types":   orEmpty(newsTypes),
		"engine_types": orEmpty(engineTypes),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode: %v", err)
	}
}

func queryInt(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		return v
	}
	return def
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
