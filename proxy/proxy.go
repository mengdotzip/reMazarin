package proxy

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/mdobak/go-xerrors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type ProxyRoute struct {
	Url       string
	Target    string
	Type      string
	Tls       bool
	Cert      string
	Key       string
	InjectAPI bool // true only for auth/admin hosts — enables built-in /api/ handlers
}

type listenServer struct {
	Port       string
	Tls        bool
	CertPath   string
	KeyPath    string
	mu         sync.RWMutex
	Routes     map[string]*ProxyRoute
	ProxyCache map[string]http.Handler
}

type Proxy struct {
	Proxies     []ProxyRoute
	servers     map[string]*listenServer
	Wg          *sync.WaitGroup
	ErrChan     chan error
	otelEnabled bool
}

func (p *Proxy) StartProxy(otel bool) ([]*http.Server, error) {
	p.otelEnabled = otel

	slog.Info("starting proxies",
		"count", len(p.Proxies),
	)

	p.servers = make(map[string]*listenServer)

	if err := p.parseProxies(); err != nil {
		return nil, xerrors.Newf("parse proxies: %w", err)
	}

	if err := p.initProxies(otel); err != nil {
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

func (p *Proxy) initProxies(otel bool) error {
	slog.Info("initializing reverse proxies")
	for port, server := range p.servers {
		for host, route := range server.Routes {
			handler, err := createHandlerForRoute(route, otel)
			if err != nil {
				return xerrors.Newf("create handler for %s: %w", route.Url, err)
			}
			server.ProxyCache[host] = handler
			slog.Debug("handler cached", "host", host, "port", port, "type", route.Type)
		}
	}
	slog.Info("all proxies initialized", "count", len(p.Proxies))
	return nil
}

func createHandlerForRoute(route *ProxyRoute, otel bool) (http.Handler, error) {
	var handler http.Handler
	var err error
	switch route.Type {
	case "static":
		handler, err = createStaticHandler(route)
	case "api":
		handler, err = createAPIHandler(route)
	case "proxy", "":
		handler, err = createReverseProxy(route)
	default:
		return nil, xerrors.Newf("unknown handler type: %s", route.Type)
	}
	if err != nil {
		return nil, err
	}
	if otel {
		handler = otelhttp.NewHandler(handler, "/")
	}
	return handler, nil
}

// RegisterRoute dynamically adds or replaces a route in a running proxy.
// Returns an error if no listener exists for the route's port; in that case
// the route is still persisted in the DB and will be active after a restart.
func (p *Proxy) RegisterRoute(route ProxyRoute) error {
	host, port, err := parseHostPort(route.Url)
	if err != nil {
		return xerrors.Newf("parse route url: %w", err)
	}
	ls, ok := p.servers[port]
	if !ok {
		return xerrors.Newf("no active listener on port %s — route saved, restart to activate", port)
	}
	handler, err := createHandlerForRoute(&route, p.otelEnabled)
	if err != nil {
		return xerrors.Newf("create handler: %w", err)
	}
	r := route
	ls.mu.Lock()
	ls.Routes[host] = &r
	ls.ProxyCache[host] = handler
	ls.mu.Unlock()
	slog.Info("route registered", "url", route.Url)
	return nil
}

// UnregisterRoute removes a route from the live proxy.
func (p *Proxy) UnregisterRoute(url string) {
	host, port, err := parseHostPort(url)
	if err != nil {
		return
	}
	ls, ok := p.servers[port]
	if !ok {
		return
	}
	ls.mu.Lock()
	delete(ls.Routes, host)
	delete(ls.ProxyCache, host)
	ls.mu.Unlock()
	slog.Info("route unregistered", "url", url)
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
