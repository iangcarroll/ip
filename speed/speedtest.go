package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	maxDownloadBytes = 1 << 30  // 1 GiB cap
	maxUploadBytes   = 1 << 30  // 1 GiB cap
	downloadDefault  = 25 << 20 // 25 MiB if ?bytes= not provided
)

// zeroBuf is reused across /__down responses. TLS encrypts on the wire so
// zeros are indistinguishable from random ciphertext to any observer.
// crypto/rand would CPU-bound a small Fly machine; this is a big perf win.
var zeroBuf = make([]byte, 64<<10)

// Per-endpoint concurrency caps so a few visitors can't saturate the single
// Fly machine. Excess requests get 503 with Retry-After.
var (
	downloadSem = make(chan struct{}, 32)
	uploadSem   = make(chan struct{}, 16)
)

func handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	select {
	case downloadSem <- struct{}{}:
		defer func() { <-downloadSem }()
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}

	n := downloadDefault
	if q := r.URL.Query().Get("bytes"); q != "" {
		v, err := strconv.Atoi(q)
		if err != nil || v < 0 {
			http.Error(w, "invalid bytes", http.StatusBadRequest)
			return
		}
		if v > maxDownloadBytes {
			v = maxDownloadBytes
		}
		n = v
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Encoding", "identity")
	w.Header().Set("Content-Length", strconv.Itoa(n))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	remaining := n
	for remaining > 0 {
		chunk := len(zeroBuf)
		if remaining < chunk {
			chunk = remaining
		}
		if _, err := w.Write(zeroBuf[:chunk]); err != nil {
			return
		}
		remaining -= chunk
	}
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	select {
	case uploadSem <- struct{}{}:
		defer func() { <-uploadSem }()
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	start := time.Now()
	received, err := io.Copy(io.Discard, r.Body)
	elapsed := time.Since(start)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"received":   received,
			"elapsed_ms": elapsed.Milliseconds(),
			"error":      err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"received":   received,
		"elapsed_ms": elapsed.Milliseconds(),
	})
}
