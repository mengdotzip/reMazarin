package proxy

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/mdobak/go-xerrors"
)

type ProxyRoute struct {
	Url    string
	Target string
	Tls    bool
}

type listenServer struct {
	Port   string
	Tls    bool
	Routes []ProxyRoute
}

type Proxy struct {
	Proxies []ProxyRoute
	servers map[string]*listenServer
	Wg      *sync.WaitGroup
	ErrChan chan error
}

func (p *Proxy) StartProxy() ([]*http.Server, error) {

	slog.Info("starting proxies",
		"count", len(p.Proxies),
	)

	p.servers = make(map[string]*listenServer)
	p.ErrChan = make(chan error, len(p.servers))

	if err := p.parseProxies(); err != nil {
		return nil, xerrors.Newf("parse proxies: %w", err)
	}

	servers := p.startListeners()
	return servers, nil
}

func (p *Proxy) parseProxies() error {

	usedUrls := make(map[string]bool)
	for _, route := range p.Proxies {
		host, port, err := parseHostPort(route.Url)
		if err != nil {
			return xerrors.Newf("parse route %s: %w", route.Url, err)
		}

		if usedUrls[route.Url] {
			return xerrors.Newf("duplicate URL configuration: %s (port %s)", host, port)
		}
		usedUrls[route.Url] = true

		if existing, exists := p.servers[port]; exists {
			if existing.Tls != route.Tls {
				return xerrors.Newf("tls configuration: cant have port %v listen on tls true and false", port)
			}

			existing.Routes = append(existing.Routes, route)
			slog.Debug("new listen server route",
				"port", port,
				"tls", route.Tls,
				"host", host,
			)
			continue
		}

		var routes []ProxyRoute
		routes = append(routes, route)
		ls := listenServer{
			Port:   port,
			Tls:    route.Tls,
			Routes: routes,
		}
		p.servers[port] = &ls

		slog.Debug("new listen server conf",
			"port", port,
			"tls", route.Tls,
			"init_route", host,
		)

	}

	return nil
}

func parseHostPort(rawURL string) (string, string, error) {
	i := strings.LastIndex(rawURL, ":")
	if i == -1 {
		return "", "", xerrors.New("parse url: no port defined")
	}

	host := rawURL[:i]
	port := rawURL[i+1:]

	return host, port, nil
}
