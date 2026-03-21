package main

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func extractClientIP(r *http.Request) netip.Addr {
	// Priority: Fly-Client-IP → X-Forwarded-For (first) → X-Real-IP → RemoteAddr
	if v := r.Header.Get("Fly-Client-IP"); v != "" {
		if addr, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
			return addr.Unmap()
		}
	}

	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		first := strings.TrimSpace(strings.SplitN(v, ",", 2)[0])
		if addr, err := netip.ParseAddr(first); err == nil {
			return addr.Unmap()
		}
	}

	if v := r.Header.Get("X-Real-IP"); v != "" {
		if addr, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
			return addr.Unmap()
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap()
	}

	return netip.Addr{}
}
