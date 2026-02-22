package proxy

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/mdobak/go-xerrors"
)

type ProxyRoute struct {
	Url    string
	Target string
	Type   string
	Tls    bool
	Cert   string
	Key    string
}

type listenServer struct {
	Port       string
	Tls        bool
	CertPath   string
	KeyPath    string
	Routes     map[string]*ProxyRoute
	ProxyCache map[string]http.Handler
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

	if err := p.parseProxies(); err != nil {
		return nil, xerrors.Newf("parse proxies: %w", err)
	}

	if err := p.initProxies(); err != nil {
		return nil, xerrors.Newf("init proxies: %w", err)
	}

	p.ErrChan = make(chan error, len(p.servers))
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

		if route.Tls {
			if route.Cert == "" || route.Key == "" {
				return xerrors.Newf("route %s has TLS enabled but missing cert/key paths", route.Url)
			}

			if _, err := os.Stat(route.Cert); err != nil {
				return xerrors.Newf("cert file not found: %s", route.Cert)
			}
			if _, err := os.Stat(route.Key); err != nil {
				return xerrors.Newf("key file not found: %s", route.Key)
			}
		}

		if existing, exists := p.servers[port]; exists {
			if existing.Tls != route.Tls {
				return xerrors.Newf("tls configuration: cant have port %v listen on tls true and false", port)
			}

			existing.Routes[host] = &route
			slog.Debug("new listen server route",
				"port", port,
				"tls", route.Tls,
				"host", host,
			)
			continue
		}

		ls := listenServer{
			Port:       port,
			Tls:        route.Tls,
			CertPath:   route.Cert,
			KeyPath:    route.Key,
			Routes:     make(map[string]*ProxyRoute),
			ProxyCache: make(map[string]http.Handler),
		}
		ls.Routes[host] = &route
		p.servers[port] = &ls

		slog.Debug("new listen server conf",
			"port", port,
			"tls", route.Tls,
			"init_route", host,
		)

	}

	return nil
}

func (p *Proxy) initProxies() error {
	slog.Info("initializing reverse proxies")

	for port, server := range p.servers {
		for host, route := range server.Routes {
			switch route.Type {
			case "static":
				handler, err := createStaticHandler(route)
				if err != nil {
					return xerrors.Newf("create handler for %s: %w", route.Url, err)
				}

				server.ProxyCache[host] = handler
				slog.Debug("static serve cached",
					"host", host,
					"port", port,
					"target", route.Target,
				)
			case "proxy", "":
				proxy, err := createReverseProxy(route)
				if err != nil {
					return xerrors.Newf("create proxy for %s: %w", route.Url, err)
				}

				server.ProxyCache[host] = proxy
				slog.Debug("proxy cached",
					"host", host,
					"port", port,
					"target", route.Target,
				)
			case "api":
				api, err := createAPIHandler(route)
				if err != nil {
					return xerrors.Newf("create api for %s: %w", route.Url, err)
				}
				server.ProxyCache[host] = api
				slog.Debug("api cached",
					"host", host,
					"port", port,
					"target", route.Target,
				)

			default:
				return xerrors.Newf("invalid config, unkown type: %s", route.Type)
			}
		}
	}

	slog.Info("all proxies initialized", "count", len(p.Proxies))
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
