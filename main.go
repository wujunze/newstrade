package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"newstrade/internal/service"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if err := service.InitDB(); err != nil {
		log.Fatalf("init db: %v", err)
	}

	if err := service.MigrateDB(); err != nil {
		log.Fatalf("migrate db: %v", err)
	}

	go service.RunSubscriber()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	log.Printf("HTTP health server on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
