package proxy

import (
	"log/slog"
	"net/http"
	"strings"
)

func (p *Proxy) route(w http.ResponseWriter, r *http.Request) {
	slog.Debug("connection to router",
		"host", r.Host,
	)

	reqHost := strings.Split(strings.ToLower(r.Host), ":")
	var currentPort string
	if len(reqHost) == 1 {
		if r.TLS == nil {
			currentPort = "80"
		} else {
			currentPort = "443"
		}
	} else {
		currentPort = reqHost[1]
	}

	// Lookup
	ls, ok := p.servers[currentPort]
	if !ok {
		slog.Debug("requested port does not exist",
			"port", currentPort,
		)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	_, ok = ls.Routes[reqHost[0]] // We dont really need this anymore, the proxy cache can look this up
	if !ok {
		slog.Debug("requested url does not exist",
			"url", reqHost[0],
		)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	proxy, ok := ls.ProxyCache[reqHost[0]]
	if !ok {
		slog.Error("proxy not cached", "host", reqHost[0])
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	slog.Debug("routing",
		"host", r.Host,
	)
	proxy.ServeHTTP(w, r)
}
