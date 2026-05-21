package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// speedConfig is the runtime view of which hosts clients should use.
// Source of truth is the ACME_DOMAIN env var (also used to provision certs).
//
// Two modes, auto-detected:
//
//   - **wildcard mode**: ACME_DOMAIN contains a "*.something" entry. Each
//     /__config call generates fresh random subdomains under that suffix —
//     per-test "random subdomains" without redeploying. Requires a wildcard
//     cert (DNS-01 via DNSimple).
//
//   - **static mode**: ACME_DOMAIN lists concrete names. /__config returns
//     them as-is (the first as ping host, the rest as shards). Used when no
//     DNS-01 provider is configured.
type speedConfig struct {
	ping           string
	staticShards   []string // used when wildcardSuffix == ""
	wildcardSuffix string   // e.g. ".speed.ian.sh" — derived from "*.speed.ian.sh"
	wildcardCount  int      // how many random shards to return per call
}

const defaultWildcardShardCount = 16

func newSpeedConfig(domains []string) *speedConfig {
	c := &speedConfig{ping: "localhost"}
	if len(domains) == 0 {
		c.staticShards = []string{"localhost"}
		return c
	}
	var bareNames []string
	for _, d := range domains {
		if strings.HasPrefix(d, "*.") && c.wildcardSuffix == "" {
			c.wildcardSuffix = strings.TrimPrefix(d, "*")
			c.wildcardCount = defaultWildcardShardCount
			continue
		}
		bareNames = append(bareNames, d)
	}
	if len(bareNames) > 0 {
		c.ping = bareNames[0]
	}
	if c.wildcardSuffix == "" {
		if len(bareNames) > 1 {
			c.staticShards = bareNames[1:]
		} else {
			c.staticShards = []string{c.ping}
		}
	}
	return c
}

func (c *speedConfig) shards() []string {
	if c.wildcardSuffix == "" {
		return c.staticShards
	}
	out := make([]string, c.wildcardCount)
	for i := range out {
		out[i] = "r-" + randomLabel(6) + c.wildcardSuffix
	}
	return out
}

func randomLabel(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *speedConfig) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if c.wildcardSuffix != "" {
		// Random subdomains regenerate per request — must not cache.
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	_ = json.NewEncoder(w).Encode(struct {
		Ping   string   `json:"ping"`
		Shards []string `json:"shards"`
	}{
		Ping:   c.ping,
		Shards: c.shards(),
	})
}
