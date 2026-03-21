package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

//go:embed index.html
var indexFS embed.FS

var indexTmpl = template.Must(template.ParseFS(indexFS, "index.html"))

func isCLI(ua string) bool {
	ua = strings.ToLower(ua)
	prefixes := []string{"curl/", "wget/", "httpie/", "fetch/", "lwp-request", "python-urllib", "go-http-client", "powershell"}
	for _, p := range prefixes {
		if strings.HasPrefix(ua, p) {
			return true
		}
	}
	return !strings.Contains(ua, "mozilla")
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	addr := extractClientIP(r)
	info := s.Lookup(addr)

	ua := r.Header.Get("User-Agent")
	if ua == "" || isCLI(ua) {
		// Serve plain text directly over HTTP — no redirect for CLI tools
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, info.IP)
		return
	}

	// Redirect browsers to HTTPS
	if !isHTTPS(r) {
		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	infoJSON, _ := json.Marshal(info)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	indexTmpl.Execute(w, template.JS(infoJSON))
}

func (s *Server) handleJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Redirect browsers to HTTPS, but serve JSON directly for CLI tools
	if !isHTTPS(r) && !isCLI(r.Header.Get("User-Agent")) {
		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	addr := extractClientIP(r)
	info := s.Lookup(addr)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(info)
}
