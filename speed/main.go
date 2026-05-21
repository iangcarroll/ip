package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	domains := splitDomains(getenv("ACME_DOMAIN", "speed.ian.sh"))
	cfg := newSpeedConfig(domains)
	mux := buildMux(cfg)
	mode := getenv("TLS_MODE", "off")

	switch mode {
	case "acme":
		if err := runTLS(ctx, mux, domains); err != nil {
			log.Fatalf("runTLS: %v", err)
		}
	default:
		port := getenv("PORT", "8080")
		log.Printf("speed: plain HTTP on :%s (TLS_MODE=off)", port)
		srv := &http.Server{Addr: ":" + port, Handler: mux}
		go func() {
			<-ctx.Done()
			_ = srv.Shutdown(context.Background())
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe: %v", err)
		}
	}
}

func buildMux(cfg *speedConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/version", handleVersion)
	mux.HandleFunc("/ping", handlePing)
	mux.HandleFunc("/__down", handleDownload)
	mux.HandleFunc("/__up", handleUpload)
	mux.HandleFunc("/__config", cfg.handleConfig)
	return withCORS(mux)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitDomains(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
