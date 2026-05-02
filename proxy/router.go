package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"reMazarin/api"
	"strings"
)

func (p *Proxy) route(w http.ResponseWriter, r *http.Request) {
	slog.Debug("connection to router",
		"host", r.Host,
	)

	// API paths are served from every route regardless of type.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		name := strings.TrimPrefix(r.URL.Path, "/api/")
		if handler, err := api.Get(name); err == nil {
			handler(w, r)
			return
		}
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

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
	withAuth(proxy).ServeHTTP(w, r)
}
