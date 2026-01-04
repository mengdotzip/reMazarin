package proxy

import (
	"log/slog"
	"net/http"
)

func (p *Proxy) startListeners() []*http.Server {

	slog.Info("starting listeners",
		"count", len(p.servers),
	)

	var listenServers []*http.Server
	for _, listener := range p.servers {
		server := p.startListener(listener)
		listenServers = append(listenServers, server)
	}
	return nil
}

func (p Proxy) startListener(listener *listenServer) *http.Server {
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:    listener.Port,
		Handler: mux,
	}

	p.Wg.Add(1)
	go p.startServe(server)
	return server
}

func (p Proxy) startServe(server *http.Server) {
	defer p.Wg.Done()

	slog.Debug("starting listen server",
		"port", server.Addr,
	)

	err := server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		//	log.Printf("HTTP Listener: ListenAndServeTLS error: %v", err)
	}
}
