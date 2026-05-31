package proxy

import (
	"log/slog"
	"net/http"

	"github.com/mdobak/go-xerrors"
)

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
		Addr:    ":" + listener.Port,
		Handler: mux,
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
