package main

import "net/http"

var allowedOrigins = map[string]bool{
	"https://ip.ian.sh":     true,
	"https://ip4.ian.sh":    true,
	"https://ip6.ian.sh":    true,
	"http://localhost:8080": true,
	"http://127.0.0.1:8080": true,
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" && allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		// Required so cross-origin callers can read nextHopProtocol from
		// PerformanceResourceTiming (used to detect H3 vs H2 in the IP frontend).
		w.Header().Set("Timing-Allow-Origin", "*")

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
