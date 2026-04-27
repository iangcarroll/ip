package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type IPInfo struct {
	IP          string `json:"ip"`
	Version     int    `json:"version"`
	City        string `json:"city,omitempty"`
	Region      string `json:"region,omitempty"`
	Country     string `json:"country,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	ASN         string `json:"asn,omitempty"`
	Org         string `json:"org,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Postal      string `json:"postal,omitempty"`
	// Per-request, never cached.
	FlyRegion  string `json:"fly_region,omitempty"`
	EdgeRegion string `json:"edge_region,omitempty"`
}

type ipinfoResponse struct {
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	City     string `json:"city"`
	Region   string `json:"region"`
	Country  string `json:"country"`
	Org      string `json:"org"`
	Postal   string `json:"postal"`
	Timezone string `json:"timezone"`
}

type cacheEntry struct {
	info    IPInfo
	expires time.Time
}

type Server struct {
	ipinfoToken string
	flyRegion   string
	httpClient  *http.Client

	mu    sync.RWMutex
	cache map[netip.Addr]cacheEntry
}

const cacheTTL = 1 * time.Hour

func NewServer(token, flyRegion string) *Server {
	return &Server{
		ipinfoToken: token,
		flyRegion:   flyRegion,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		cache: make(map[netip.Addr]cacheEntry),
	}
}

func (s *Server) Lookup(addr netip.Addr) IPInfo {
	info := IPInfo{
		IP: addr.String(),
	}
	if addr.Is4() {
		info.Version = 4
	} else {
		info.Version = 6
	}

	if s.ipinfoToken == "" {
		return info
	}

	// Check cache
	s.mu.RLock()
	if entry, ok := s.cache[addr]; ok && time.Now().Before(entry.expires) {
		s.mu.RUnlock()
		return entry.info
	}
	s.mu.RUnlock()

	url := fmt.Sprintf("https://ipinfo.io/%s?token=%s", addr.String(), s.ipinfoToken)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return info
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return info
	}

	var data ipinfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return info
	}

	info.City = data.City
	info.Region = data.Region
	info.CountryCode = data.Country
	info.Hostname = data.Hostname
	info.Postal = data.Postal
	info.Timezone = data.Timezone

	// Parse "AS15169 Google LLC" → ASN + Org
	if data.Org != "" {
		if strings.HasPrefix(data.Org, "AS") {
			parts := strings.SplitN(data.Org, " ", 2)
			info.ASN = parts[0]
			if len(parts) > 1 {
				info.Org = parts[1]
			}
		} else {
			info.Org = data.Org
		}
	}

	// Store in cache
	s.mu.Lock()
	s.cache[addr] = cacheEntry{info: info, expires: time.Now().Add(cacheTTL)}
	s.mu.Unlock()

	return info
}
