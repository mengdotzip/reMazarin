package proxy

import (
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/mdobak/go-xerrors"
)

// tlsErrWriter captures connection-level errors that net/http reports to
// Server.ErrorLog before any handler runs — most importantly TLS handshake
// failures (plain HTTP to a TLS port, junk bytes, scans). These never reach the
// router, so this is the only place to surface "failed to upgrade to TLS" in the
// metrics. The remote IP is parsed from Go's fixed log line:
//
//	http: TLS handshake error from <ip>:<port>: <error>
type tlsErrWriter struct{ port string }

func (w tlsErrWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	const marker = "TLS handshake error from "
	if i := strings.Index(line, marker); i >= 0 {
		rest := line[i+len(marker):]
		addr := rest
		if j := strings.Index(rest, ": "); j >= 0 {
			addr = rest[:j]
		}
		ip, _, err := net.SplitHostPort(strings.TrimSpace(addr))
		if err != nil {
			ip = strings.TrimSpace(addr)
		}
		RecordEvent(ip, ":"+w.port, OutcomeTLSError)
		RecordFailure(ip)
	}
	slog.Debug("http server error", "port", w.port, "msg", line)
	return len(p), nil
}

func (p *Proxy) startListeners() {
	slog.Info("starting listeners", "count", len(p.servers))
	for _, listener := range p.servers {
		if err := p.startListener(listener); err != nil {
			p.ErrChan <- err
			break
		}
	}
}

func (p *Proxy) startListener(listener *listenServer) error {
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:     ":" + listener.Port,
		Handler:  mux,
		ErrorLog: log.New(tlsErrWriter{port: listener.Port}, "", 0),
	}

	if listener.Tls {
		tlsConfig, err := createTLSConfig(listener.CertPath, listener.KeyPath)
		if err != nil {
			slog.Error("failed to create TLS config", "port", listener.Port, "error", err)
			return xerrors.Newf("TLS config for port %s: %w", listener.Port, err)
		}
		server.TLSConfig = tlsConfig
		slog.Info("TLS configured for listener", "port", listener.Port, "min_version", "TLS 1.2")
	}

	mux.HandleFunc("/", p.route)

	p.serversMu.Lock()
	p.liveHTTP = append(p.liveHTTP, server)
	p.serversMu.Unlock()

	p.Wg.Add(1)
	go p.startServe(server, listener.Tls)
	return nil
}

func (p *Proxy) startServe(server *http.Server, useTLS bool) {
	defer p.Wg.Done()

	slog.Debug("starting listen server", "port", server.Addr)

	var err error
	if useTLS {
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}

	if err != nil && err != http.ErrServerClosed {
		p.ErrChan <- xerrors.Newf("server %s failed: %w", server.Addr, err)
		return
	}

	slog.Debug("closing listen server", "port", server.Addr)
}
