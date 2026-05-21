# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository.

## Repo layout

Two Fly.io apps in one repo, each with its own `go.mod`, `Dockerfile`, `fly.toml`:

```
./                      ip.ian.sh         — what-is-my-IP page + JSON API
./speed/                speed.ian.sh      — HTTP/3 speed test backend (random shards under *.speed.ian.sh)
```

`./index.html` is embedded into the IP app via `//go:embed` and is the **only** UI; the speed app is API-only.

## ip.ian.sh

Single Go binary, stdlib only (zero non-stdlib deps). Four source files:

- `ip.go` — `extractClientIP(r)` honors `Fly-Client-IP` → `X-Forwarded-For` → `X-Real-IP` → `RemoteAddr`. Unmaps IPv4-mapped IPv6.
- `geo.go` — `Server.Lookup(addr)` hits ipinfo.io with a 1 hr in-memory cache (`sync.RWMutex`). Parses `"AS15169 Google LLC"` org strings into ASN + name.
- `handlers.go` — `GET /` (plain text for CLI by User-Agent; HTML for browsers), `GET /json` (CORS), `GET /ping` (204 for latency probes), `GET /health`. Browsers get HTTPS redirect; CLI tools stay on HTTP.
- `main.go` — bootstrap; routes; ListenAndServe.

Build / run:
```bash
go build -o ipserver . && IPINFO_TOKEN=... ./ipserver         # :8080
fly deploy -a ip-ian-sh                                        # prod
```

`IPINFO_TOKEN` is set via `fly secrets set IPINFO_TOKEN=...`. Without it, geo lookups degrade silently.

### Frontend (`index.html`)

Vanilla ES5, no frameworks. Go template injects `IPInfo` as `window.__IP__`. In order:

1. Theme toggle (localStorage).
2. Render primary IP from `__IP__`.
3. Fetch the alternate protocol's IP via CORS from `ip4.ian.sh` / `ip6.ian.sh`.
4. Latency probes — every 1 s, separate v4 + v6 against `/ping` on each subdomain; median + min.
5. IP lookup form — calls `/json?ip=…`.
6. **Speed test** — gated on IPv4 reachability (decoupled from #3, see below). Calls `speed.ian.sh`.

### Dual-stack via DNS

The binary is identical on all three hostnames; DNS does protocol selection:

| Host | Records | Purpose |
|---|---|---|
| `ip.ian.sh` | A + AAAA | Happy Eyeballs |
| `ip4.ian.sh` | A only | Force v4 |
| `ip6.ian.sh` | AAAA only | Force v6 |

## speed.ian.sh

Separate Go app in `./speed/`. Terminates TLS itself for **HTTP/2 over TCP/443** *and* **HTTP/3 over UDP/443** (fly-proxy passthrough). Provides high-throughput download/upload endpoints + a config endpoint the IP frontend bootstraps from.

### Architecture summary

- **Dedicated IPv4 only** (`137.66.19.20`) — no AAAA. Fly's edge does not route UDP on v6, so HTTP/3 wouldn't work over v6 anyway. v6-only clients can't use the speed test (frontend hides the button).
- **Wildcard DNS**: `A *.speed.ian.sh → 137.66.19.20`. Any subdomain resolves to the same machine.
- **Wildcard HTTPS RR (RFC 9460)**: `HTTPS *.speed.ian.sh 1 . alpn="h3,h2"`. Modern browsers learn H3 support from DNS itself — first request to a fresh random shard uses H3 directly, no Alt-Svc round-trip required.
- **Wildcard TLS cert** via Let's Encrypt + DNS-01 challenge using DNSimple (`libdns/dnsimple`). Cert covers `*.speed.ian.sh` + bare `speed.ian.sh`. Requires `DNSIMPLE_TOKEN` Fly secret.
- **Random shard names** per test: `/__config` returns 16 fresh `r-<12-hex>.speed.ian.sh` names. Each name becomes an independent QUIC connection in the browser, defeating connection-level pooling and giving each its own congestion controller — the only way to actually saturate a multi-hundred-Mbps link from one backend machine.
- **certmagic + Fly volume** (`/data/certmagic`) for cert storage and renewal.

### Source files

- `main.go` — env, mux, branches on `TLS_MODE=acme|off`.
- `tls.go` — `setupTLS()` configures certmagic. If `DNSIMPLE_TOKEN` is present, switches to DNS-01 (required for wildcards) and disables TLS-ALPN-01. Pins both `CA` and `TestCA` to LE production (default fallback would silently issue staging certs).
- `listeners.go` — binds TCP/443 (`tcp4`) and UDP/443 (`udp4` on `fly-global-services`). Bumps `udpConn.SetReadBuffer/SetWriteBuffer` to 16 MiB. Single `*tls.Config` from certmagic feeds both `http.Server` and `http3.Server`. `withAltSvc` middleware advertises h3 on every response (also useful for clients that don't honor HTTPS RR).
- `speedtest.go` — `/__down` streams a reused 64 KiB zero buffer (TLS encrypts the wire, so observers can't tell; `crypto/rand` would CPU-bound the machine). `/__up` drains `io.Discard`. Both have per-endpoint concurrency caps (32 / 16) that return 503 when full, and `http.MaxBytesReader` caps body size at 1 GiB.
- `health.go` — `/health`, `/ping`, `/version`.
- `cors.go` — origin allowlist for `ip.ian.sh`, `ip4.ian.sh`, `ip6.ian.sh`, `localhost:8080`. Adds `Timing-Allow-Origin: *` so the IP frontend can read `nextHopProtocol` from cross-origin `PerformanceResourceTiming`.
- `config.go` — `/__config` returns `{ping, shards}`. Auto-detects mode from `ACME_DOMAIN`: if any entry begins `*.`, it's wildcard mode (random per-request names, `no-store`). Otherwise static mode (fixed list, 5-min cache).

### QUIC tuning

quic-go's defaults bottleneck cold connections to ~50 Mbps. In `listeners.go`:

```go
QUICConfig: &quic.Config{
    Allow0RTT:                      true,
    InitialStreamReceiveWindow:     8 << 20,   // 8 MiB
    MaxStreamReceiveWindow:        64 << 20,   // 64 MiB
    InitialConnectionReceiveWindow: 16 << 20,  // 16 MiB
    MaxConnectionReceiveWindow:   128 << 20,   // 128 MiB
}
```

Also: `Dockerfile`'s entrypoint runs `sysctl -w net.core.{r,w}mem_max=16777216` before exec'ing the binary. Fly's Firecracker kernel ships with 208 KiB defaults, which silently throttle QUIC above ~100 Mbps. The sysctl raises the kernel max so `udpConn.SetReadBuffer(16 MiB)` actually takes effect.

quic-go currently only implements NewReno (no BBR). At ~70 ms RTT, slow-start needs ~1 s before reaching steady-state cwnd — the frontend's 2 s warmup window is sized for this.

### Browser-side behavior (in `index.html`)

The speed test code uses **`speed.ian.sh` only for bootstrap and ping**. For the timed transfer it uses `/__config`'s `shards`:

1. **Visibility** (`ensureSpeedVisibility`): shows the button immediately if the page itself was loaded over v4 (`__IP__.version === 4` or `host === 'ip4.ian.sh'`). Otherwise it probes `speed.ian.sh/ping` with a 3 s AbortController timeout and decides. **Decoupled from the alt-IP fetch loop** so a hanging v6 probe can't delay the button.
2. **Config bootstrap** (`loadSpeedConfig`): fetches `speed.ian.sh/__config` and shuffles `cfg.shards` (so successive runs use different orderings, and in wildcard mode different *names*). Builds a runtime `SPEED_HOST_RE` covering bootstrap + current shards.
3. **Warmup** (`prewarmShards`): one parallel `/ping` per shard. Even though the wildcard HTTPS RR tells the browser to use H3 from request 1, the QUIC handshake (~1 RTT) still has to complete. This round pre-opens the H3 connections so the timed transfer starts on warm connections rather than burning the first 100-200 ms on handshakes. Also serves as an Alt-Svc-discovery fallback for browsers that don't honor HTTPS RR.
4. **Ping** (`measurePing`): 8 sequential GETs to `speed.ian.sh/ping`, computes median + jitter (stddev).
5. **Download** (`measureDownload`): K parallel `fetch()` to `<shard>/__down?bytes=500MB`, each consumes `response.body.getReader()` in a loop. Aggregate Mbps computed over `[startedAt+2s, end]`. K = `SPEED_SHARDS.length`.
6. **Upload** (`measureUpload`): K parallel `XMLHttpRequest` POST of a 25 MiB `Uint8Array` to `<shard>/__up`, looped per stream until aborted. XHR is used (not fetch) because `xhr.upload.onprogress` gives byte-level progress; fetch streaming uploads have poor cross-browser support.
7. **Protocol detection**: `PerformanceObserver` watches `resource` entries; filters by the runtime-built `SPEED_HOST_RE`; reads `nextHopProtocol` to label each phase (`h3` / `h2`).

Changing `defaultWildcardShardCount` in `speed/config.go` controls parallelism without any frontend redeploy.

### Build / deploy

```bash
cd speed
go build -o /tmp/speedserver .                   # local
TLS_MODE=off PORT=8081 /tmp/speedserver           # plain HTTP for local dev

fly deploy -a ip-speed                            # prod
```

Local dev never touches Let's Encrypt or `:443` (no privileges needed); `TLS_MODE` defaults to `off`.

### Fly secrets (speed app)

- `ACME_EMAIL` — LE registration contact
- `DNSIMPLE_TOKEN` — DNSimple API token for DNS-01 (`fly secrets import -a ip-speed --stage`)

### Deferred / known issues

- **Multi-region**: single SJC machine. Adding regions requires CID-aware UDP demuxing (QUIC connection migration across machines is unsolved on Fly because edge UDP routing is 5-tuple hashed). The `quic.Transport{Conn: udpConn}` API leaves the hook open for a future per-machine demuxer + 6PN forwarder.
- **Congestion control**: quic-go is NewReno-only. BBR would close the slow-start gap vs Cloudflare/Ookla but isn't pluggable without forking.
- **Adaptive concurrency**: K is static. Could ramp like fast.com (start at 2, add streams while throughput is still climbing).

## Common operations

```bash
# Status of both apps
fly status -a ip-ian-sh
fly status -a ip-speed

# Tail logs
fly logs -a ip-speed

# Verify wildcard cert end-to-end with a fresh random name
RAND="r-$(openssl rand -hex 6).speed.ian.sh"
echo | openssl s_client -connect "$RAND:443" -servername "$RAND" 2>/dev/null \
  | openssl x509 -noout -subject -issuer

# Inspect /__config (random shards regenerate each call in wildcard mode)
curl -sS https://speed.ian.sh/__config | python3 -m json.tool

# Confirm wildcard HTTPS RR is live (TYPE65 = HTTPS)
dig TYPE65 "r-$(openssl rand -hex 4).speed.ian.sh" @1.1.1.1 +noall +answer

# Sanity-check current UDP buffer sysctl values inside the speed VM
fly ssh console -a ip-speed -C "sysctl net.core.rmem_max net.core.wmem_max"
```
