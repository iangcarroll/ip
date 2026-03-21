# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o ipserver .    # build binary
./ipserver                # run locally on :8080
```

Requires `IPINFO_TOKEN` env var for geo/ASN lookups (gracefully degrades without it). Port configurable via `PORT` env var.

Deploy: `fly deploy` (token set via `fly secrets set IPINFO_TOKEN=...`)

## Architecture

Single Go binary, zero dependencies beyond stdlib and `github.com/oschwald/geoip2-golang` (unused, may be removed). Four source files:

- **`ip.go`** — `extractClientIP(r)` extracts client IP from headers (`Fly-Client-IP` → `X-Forwarded-For` → `X-Real-IP` → `RemoteAddr`), unmaps IPv4-mapped IPv6
- **`geo.go`** — `Server.Lookup(addr)` calls ipinfo.io API with in-memory cache (1hr TTL, `sync.RWMutex`). Parses org field ("AS15169 Google LLC" → ASN + org name)
- **`handlers.go`** — Three routes: `GET /` (plain text for CLI, HTML for browsers), `GET /json` (CORS-enabled JSON API), `GET /health`. CLI detection via User-Agent. Browsers get HTTPS redirect; CLI tools served directly over HTTP
- **`main.go`** — Bootstrap: creates Server, registers routes, listens

## Frontend

`index.html` is embedded at compile time via `//go:embed`. The template injects `IPInfo` as `window.__IP__` JSON. Browser JS then fetches the alternate protocol IP (IPv4↔IPv6) via CORS to `ip4.ian.sh`/`ip6.ian.sh` subdomains.

## Dual-Stack Design

DNS does protocol selection — the Go app is identical for all hostnames:
- `ip.ian.sh` — A + AAAA (Happy Eyeballs)
- `ip4.ian.sh` — A only (forces IPv4)
- `ip6.ian.sh` — AAAA only (forces IPv6)
