package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"newstrade/internal/service"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Validate required config up front so a misconfiguration fails fast and
	// loudly instead of crashing a background goroutine after startup.
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

	// Root context cancelled on SIGINT/SIGTERM for graceful shutdown.
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

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
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

	log.Printf("HTTP health server on :%s", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http server: %v", err)
	}

	// Wait for the subscriber to unwind before exiting so deferred CloseDB runs
	// only after in-flight writes settle.
	wg.Wait()
	log.Println("shutdown complete")
}

// healthHandler gates liveness on database reachability — the hard dependency
// the process cannot function without. The WSS subscriber reconnects on its own,
// so its status is reported for observability but does not fail the check
// (failing it would only trigger counterproductive restart loops on transient
// feed outages or auth issues a restart can't fix).
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
