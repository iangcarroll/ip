# Build Go binary
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG BUILD_SHA=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.buildSHA=${BUILD_SHA}" -o /bin/speedserver .

# Minimal runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/speedserver /bin/speedserver
# Raise UDP socket buffer kernel maxes; quic-go's SetReadBuffer call can't
# exceed these. Fly's Firecracker kernel default is ~208 KiB which silently
# caps QUIC throughput at ~100 Mbps. The `|| true` keeps non-Fly local runs
# (no CAP_NET_ADMIN) working.
CMD ["sh", "-c", "sysctl -w net.core.rmem_max=16777216 net.core.wmem_max=16777216 > /dev/null 2>&1 || true; exec /bin/speedserver"]
