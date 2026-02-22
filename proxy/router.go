package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
)

func (p *Proxy) route(w http.ResponseWriter, r *http.Request) {
	slog.Debug("connection to router",
		"host", r.Host,
	)

	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = strings.ToLower(r.Host)
		if r.TLS != nil {
			port = "443"
		} else {
			port = "80"
		}
	}
	host = strings.ToLower(host)

	// Lookup
	ls, ok := p.servers[port]
	if !ok {
		slog.Debug("requested port does not exist",
			"port", port,
		)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	_, ok = ls.Routes[host] // We dont really need this anymore, the proxy cache can look this up
	if !ok {
		slog.Debug("requested url does not exist",
			"url", host,
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	proxy, ok := ls.ProxyCache[host]
	if !ok {
		slog.Error("proxy not cached", "host", host)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	slog.Debug("routing",
		"host", r.Host,
	)
	proxy.ServeHTTP(w, r)
}
