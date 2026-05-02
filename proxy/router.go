package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"reMazarin/api"
	"strings"
)

func (p *Proxy) route(w http.ResponseWriter, r *http.Request) {
	slog.Debug("connection to router", "host", r.Host)

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

	ls, ok := p.servers[port]
	if !ok {
		slog.Debug("requested port does not exist", "port", port)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	ls.mu.RLock()
	route, routeFound := ls.Routes[host]
	handler, handlerFound := ls.ProxyCache[host]
	ls.mu.RUnlock()

	if !routeFound {
		slog.Debug("requested url does not exist", "url", host)
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	if !handlerFound {
		slog.Error("proxy not cached", "host", host)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Built-in /api/ handlers (auth, admin) are injected only for the hosts
	// explicitly marked InjectAPI at startup (the [web] and [admin] hosts).
	// All other routes — including static ones — must have /api/ forwarded
	// to their backend unchanged.
	if route.InjectAPI && strings.HasPrefix(r.URL.Path, "/api/") {
		name := strings.TrimPrefix(r.URL.Path, "/api/")
		if apiHandler, err := api.Get(name); err == nil {
			apiHandler(w, r)
			return
		}
	}

	slog.Debug("routing", "host", r.Host)
	withAuth(handler).ServeHTTP(w, r)
}
