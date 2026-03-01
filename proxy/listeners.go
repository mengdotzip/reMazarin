package proxy

import (
	"log/slog"
	"net/http"

	"github.com/mdobak/go-xerrors"
)

func (p *Proxy) startListeners() []*http.Server {

	slog.Info("starting listeners",
		"count", len(p.servers),
	)

	var listenServers []*http.Server
	for _, listener := range p.servers {
		server, err := p.startListener(listener)
		if err != nil {
			break
		}
		listenServers = append(listenServers, server)
	}
	return listenServers
}

func (p *Proxy) startListener(listener *listenServer) (*http.Server, error) {
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:    ":" + listener.Port,
		Handler: mux,
	}

	if listener.Tls {
		tlsConfig, err := createTLSConfig(listener.CertPath, listener.KeyPath)
		if err != nil {
			slog.Error("failed to create TLS config",
				"port", listener.Port,
				"error", err,
			)
			p.ErrChan <- xerrors.Newf("TLS config for port %s: %w", listener.Port, err)
			return nil, xerrors.Newf("TLS config for port %s: %w", listener.Port, err)
		}
		server.TLSConfig = tlsConfig

		slog.Info("TLS configured for listener",
			"port", listener.Port,
			"min_version", "TLS 1.2",
		)
	}

	mux.HandleFunc("/", p.route)
	p.Wg.Add(1)
	go p.startServe(server, listener.Tls)
	return server, nil
}

func (p *Proxy) startServe(server *http.Server, useTLS bool) {
	defer p.Wg.Done()

	slog.Debug("starting listen server",
		"port", server.Addr,
	)

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

	slog.Debug("closing listen server",
		"port", server.Addr,
	)
}
