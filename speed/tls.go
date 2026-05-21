package main

import (
	"context"
	"crypto/tls"
	"log"
	"os"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/dnsimple"
)

func setupTLS(ctx context.Context, domains []string) (*tls.Config, error) {
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = getenv("ACME_EMAIL", "")
	if certmagic.DefaultACME.Email == "" {
		log.Println("WARN: ACME_EMAIL is unset; ACME registration may fail")
	}

	storagePath := getenv("CERT_DIR", "/data/certmagic")
	if err := os.MkdirAll(storagePath, 0700); err != nil {
		return nil, err
	}
	certmagic.Default.Storage = &certmagic.FileStorage{Path: storagePath}

	// We don't run :80, so HTTP-01 cannot work.
	certmagic.DefaultACME.DisableHTTPChallenge = true

	// If a DNSimple API token is set, use DNS-01 — required for wildcard
	// certs (TLS-ALPN-01 cannot issue wildcards). Disable TLS-ALPN-01 in
	// that case so we don't double-challenge.
	if token := os.Getenv("DNSIMPLE_TOKEN"); token != "" {
		certmagic.DefaultACME.DNS01Solver = &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: &dnsimple.Provider{APIAccessToken: token},
			},
		}
		certmagic.DefaultACME.DisableTLSALPNChallenge = true
		log.Println("certmagic: using DNS-01 via DNSimple (wildcard certs enabled)")
	}

	// Pin both primary and fallback CAs to Let's Encrypt production. By
	// default certmagic falls back to LE staging after enough failures,
	// which produces untrusted certs (and the fallback persists once it
	// triggers).
	certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA
	certmagic.DefaultACME.TestCA = certmagic.LetsEncryptProductionCA

	cm := certmagic.NewDefault()

	// Try to acquire/load certs synchronously on cold start so the first
	// request post-deploy doesn't hit a TLS error. On warm boots with a
	// populated volume this returns immediately with a cached cert. 120s
	// gives headroom for many-name issuance — TLS-ALPN-01 takes 2-7s per
	// name and we issue one cert per shard.
	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err := cm.ManageSync(cctx, domains); err != nil {
		log.Printf("certmagic ManageSync failed (retrying async): %v", err)
		if err := cm.ManageAsync(ctx, domains); err != nil {
			return nil, err
		}
	}

	tlsCfg := cm.TLSConfig()
	// Prepend h3/h2/http/1.1 while preserving certmagic's acme-tls/1 entry
	// (TLS-ALPN-01 challenges require it).
	tlsCfg.NextProtos = append([]string{"h3", "h2", "http/1.1"}, tlsCfg.NextProtos...)
	return tlsCfg, nil
}
