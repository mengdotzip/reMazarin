package proxy

import (
	"log/slog"
	"net"
	"net/http"
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

	clientIP := extractClientIP(r)

	// Drop banned IPs before any routing work, even on junk Hosts/ports.
	if IsBanned(clientIP) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		RecordEvent(clientIP, host+":"+port, OutcomeBanned)
		return
	}

	ls, ok := p.servers[port]
	if !ok {
		slog.Debug("requested port does not exist", "port", port)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		RecordEvent(clientIP, host+":"+port, OutcomeNoListener)
		RecordFailure(clientIP)
		return
	}

	handlers := ls.handlers.Load().(map[string]http.Handler)
	handler, ok := handlers[host]
	if !ok {
		slog.Debug("requested url does not exist", "url", host)
		http.Error(w, "Not Found", http.StatusNotFound)
		RecordEvent(clientIP, host+":"+port, OutcomeNotFound)
		RecordFailure(clientIP)
		return
	}

	slog.Debug("routing", "host", r.Host)
	handler.ServeHTTP(w, r)
}
