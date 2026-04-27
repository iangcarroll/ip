package main

import (
	"log"
	"net/http"
	"os"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	token := os.Getenv("IPINFO_TOKEN")
	if token == "" {
		log.Printf("Warning: IPINFO_TOKEN not set, geo lookups will be disabled")
	}

	s := NewServer(token)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/json", s.handleJSON)
	mux.HandleFunc("/ping", s.handlePing)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	addr := envOr("PORT", "8080")
	log.Printf("Listening on :%s", addr)
	log.Fatal(http.ListenAndServe(":"+addr, mux))
}
