package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func runTLS(ctx context.Context, mux http.Handler, domains []string) error {
	tlsCfg, err := setupTLS(ctx, domains)
	if err != nil {
		return err
	}

	handler := withAltSvc(mux)

	// TCP/443 — v4 only (speed.ian.sh has no AAAA record).
	tcpLn, err := net.Listen("tcp4", ":443")
	if err != nil {
		return err
	}
	h2srv := &http.Server{
		Handler:   handler,
		TLSConfig: tlsCfg.Clone(),
	}

	// UDP/443 — v4 only. Bound to fly-global-services so Fly's eBPF rewrites
	// reply source IPs correctly. Falls back to 0.0.0.0 for local dev.
	udpHost := flyUDPBindHost()
	udpAddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(udpHost, "443"))
	if err != nil {
		return err
	}
	udpConn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return err
	}
	// Push UDP socket buffers above the 208 KiB default; quic-go silently
	// drops packets above ~100 Mbps otherwise. Kernel rmem_max/wmem_max must
	// be raised first via the container entrypoint (sysctl).
	const wantUDPBuf = 16 << 20
	if err := udpConn.SetReadBuffer(wantUDPBuf); err != nil {
		log.Printf("UDP SetReadBuffer: %v", err)
	}
	if err := udpConn.SetWriteBuffer(wantUDPBuf); err != nil {
		log.Printf("UDP SetWriteBuffer: %v", err)
	}
	tr := &quic.Transport{Conn: udpConn}

	h3srv := &http3.Server{
		Handler:   handler,
		TLSConfig: tlsCfg.Clone(),
		QUICConfig: &quic.Config{
			Allow0RTT: true,
			// BDP at 1 Gbps × 100 ms ≈ 12.5 MiB; default 512 KiB / 1.5 MiB
			// initial windows bottleneck cold connections to ~50 Mbps.
			InitialStreamReceiveWindow:     8 << 20,
			MaxStreamReceiveWindow:         64 << 20,
			InitialConnectionReceiveWindow: 16 << 20,
			MaxConnectionReceiveWindow:     128 << 20,
		},
	}
	qln, err := tr.ListenEarly(http3.ConfigureTLSConfig(h3srv.TLSConfig), h3srv.QUICConfig)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		log.Printf("speed: TCP/443 listening (H1/H2 + ACME-TLS)")
		if err := h2srv.ServeTLS(tcpLn, "", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("h2 ServeTLS: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		log.Printf("speed: UDP/443 listening (H3) on %s", udpAddr)
		if err := h3srv.ServeListener(qln); err != nil && err != http.ErrServerClosed {
			log.Printf("h3 ServeListener: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		log.Printf("speed: shutting down")
		_ = h2srv.Shutdown(context.Background())
		_ = h3srv.Close()
		_ = tr.Close()
	}()

	wg.Wait()
	return nil
}

// flyUDPBindHost resolves the address quic-go should bind to. On Fly, UDP
// must bind to the address from "fly-global-services" so the edge eBPF
// rewrites source IPs correctly. Off Fly, use 0.0.0.0.
func flyUDPBindHost() string {
	if os.Getenv("FLY_REGION") == "" {
		return "0.0.0.0"
	}
	ips, err := net.LookupIP("fly-global-services")
	if err != nil {
		log.Printf("lookup fly-global-services failed: %v; falling back to 0.0.0.0", err)
		return "0.0.0.0"
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
	}
	return "0.0.0.0"
}

const altSvcValue = `h3=":443"; ma=86400`

func withAltSvc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", altSvcValue)
		next.ServeHTTP(w, r)
	})
}
