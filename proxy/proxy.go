package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"reMazarin/api"
	"strings"
	"sync"
	"sync/atomic"

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
	Port     string
	Tls      bool
	CertPath string
	KeyPath  string
	mu       sync.Mutex // serialises writes to Routes; hot-path reads use handlers
	Routes   map[string]*ProxyRoute
	handlers atomic.Value // stores map[string]http.Handler
}

type Proxy struct {
	Proxies     []ProxyRoute
	servers     map[string]*listenServer
	tcpCancels  map[string]context.CancelFunc
	tcpMu       sync.Mutex
	udpCancels  map[string]context.CancelFunc
	udpMu       sync.Mutex
	serversMu   sync.Mutex
	liveHTTP    []*http.Server
	ctx         context.Context
	Wg          *sync.WaitGroup
	ErrChan     chan error
	otelEnabled bool
}

func (p *Proxy) StartProxy(ctx context.Context, otel bool) error {
	p.ctx = ctx
	p.otelEnabled = otel

	slog.Info("starting proxies", "count", len(p.Proxies))

	p.servers = make(map[string]*listenServer)
	p.tcpCancels = make(map[string]context.CancelFunc)
	p.udpCancels = make(map[string]context.CancelFunc)

	if err := p.parseProxies(); err != nil {
		return xerrors.Newf("parse proxies: %w", err)
	}

	if err := p.initProxies(otel); err != nil {
		return xerrors.Newf("init proxies: %w", err)
	}

	rawCount := 0
	for _, r := range p.Proxies {
		if isTCP(r.Type) {
			rawCount++
		}
		if isUDP(r.Type) {
			rawCount++
		}
	}
	p.ErrChan = make(chan error, len(p.servers)+rawCount+16)

	p.startListeners()

	for _, route := range p.Proxies {
		_, port, _ := parseHostPort(route.Url)
		if isTCP(route.Type) {
			p.startTCPProxy(port, route.Target, route.Url)
		}
		if isUDP(route.Type) {
			p.startUDPProxy(port, route.Target, route.Url)
		}
	}

	return nil
}

// ShutdownHTTP gracefully shuts down all live HTTP listeners.
func (p *Proxy) ShutdownHTTP(ctx context.Context) {
	p.serversMu.Lock()
	servers := make([]*http.Server, len(p.liveHTTP))
	copy(servers, p.liveHTTP)
	p.serversMu.Unlock()
	for _, s := range servers {
		if err := s.Shutdown(ctx); err != nil {
			slog.Warn("server shutdown error", "addr", s.Addr, "error", err)
		}
	}
}

// ValidateRoute checks whether a new route is compatible with the live proxy:
// valid host:port format, known type, and no port conflicts. A raw TCP listener
// and an HTTP listener cannot share a port (both bind TCP), but a UDP listener is
// independent and may coexist with either — which is what lets a "tcp+udp" route
// (e.g. coturn on 3478) bind both protocols on the same port.
func (p *Proxy) ValidateRoute(url, routeType string) error {
	_, port, err := parseHostPort(url)
	if err != nil {
		return xerrors.Newf("invalid url %q: expected host:port", url)
	}
	switch routeType {
	case "proxy", "tcp", "udp", "tcp+udp", "static", "api", "":
	default:
		return xerrors.Newf("unknown route type %q", routeType)
	}

	p.tcpMu.Lock()
	_, hasTCP := p.tcpCancels[port]
	p.tcpMu.Unlock()
	p.udpMu.Lock()
	_, hasUDP := p.udpCancels[port]
	p.udpMu.Unlock()
	_, hasHTTP := p.servers[port]

	needsTCP := isTCP(routeType)
	needsUDP := isUDP(routeType)
	isHTTP := !isRaw(routeType)

	if (needsTCP || isHTTP) && hasTCP {
		return xerrors.Newf("port %s is already used by a TCP route", port)
	}
	if needsTCP && hasHTTP {
		return xerrors.Newf("port %s is already used by an HTTP route", port)
	}
	if needsUDP && hasUDP {
		return xerrors.Newf("port %s is already used by a UDP route", port)
	}
	return nil
}

func (p *Proxy) parseProxies() error {

	usedUrls := make(map[string]bool)
	tcpPorts := make(map[string]bool)
	udpPorts := make(map[string]bool)

	for _, route := range p.Proxies {
		host, port, err := parseHostPort(route.Url)
		if err != nil {
			return xerrors.Newf("parse route %s: %w", route.Url, err)
		}

		if usedUrls[route.Url] {
			return xerrors.Newf("duplicate URL configuration: %s (port %s)", host, port)
		}
		usedUrls[route.Url] = true

		// Raw routes (tcp / udp / tcp+udp) get dedicated per-port listeners started
		// in StartProxy rather than a shared HTTP listenServer.
		if isRaw(route.Type) {
			if isTCP(route.Type) {
				if tcpPorts[port] {
					return xerrors.Newf("duplicate TCP port %s", port)
				}
				if _, httpExists := p.servers[port]; httpExists {
					return xerrors.Newf("port %s used by both HTTP and TCP routes", port)
				}
				tcpPorts[port] = true
			}
			if isUDP(route.Type) {
				if udpPorts[port] {
					return xerrors.Newf("duplicate UDP port %s", port)
				}
				udpPorts[port] = true
			}
			slog.Debug("new raw route", "port", port, "type", route.Type, "target", route.Target)
			continue
		}

		if tcpPorts[port] {
			return xerrors.Newf("port %s used by both TCP and HTTP routes", port)
		}

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
			Port:     port,
			Tls:      route.Tls,
			CertPath: route.Cert,
			KeyPath:  route.Key,
			Routes:   make(map[string]*ProxyRoute),
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
		m := make(map[string]http.Handler, len(server.Routes))
		for host, route := range server.Routes {
			raw, err := createHandlerForRoute(route, otel)
			if err != nil {
				return xerrors.Newf("create handler for %s: %w", route.Url, err)
			}
			m[host] = wrapRouteHandler(route, raw)
			slog.Debug("handler cached", "host", host, "port", port, "type", route.Type)
		}
		server.handlers.Store(m)
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
// For raw routes (tcp / udp / tcp+udp) dedicated per-protocol listeners are
// started on the route's port. For HTTP routes, returns an error if no listener
// exists for the route's port; in that case the route is still persisted in the
// DB and will be active after a restart.
func (p *Proxy) RegisterRoute(route ProxyRoute) error {
	host, port, err := parseHostPort(route.Url)
	if err != nil {
		return xerrors.Newf("parse route url: %w", err)
	}

	if isRaw(route.Type) {
		if isTCP(route.Type) {
			p.startTCPProxy(port, route.Target, route.Url)
		}
		if isUDP(route.Type) {
			p.startUDPProxy(port, route.Target, route.Url)
		}
		slog.Info("raw route registered", "url", route.Url, "type", route.Type)
		return nil
	}

	ls, ok := p.servers[port]
	if !ok {
		// Check no TCP listener already owns this port.
		p.tcpMu.Lock()
		_, hasTCP := p.tcpCancels[port]
		p.tcpMu.Unlock()
		if hasTCP {
			return xerrors.Newf("port %s is already used by a TCP route", port)
		}
		ls = &listenServer{
			Port:     port,
			Tls:      route.Tls,
			CertPath: route.Cert,
			KeyPath:  route.Key,
			Routes:   make(map[string]*ProxyRoute),
		}
		ls.handlers.Store(make(map[string]http.Handler))
		p.servers[port] = ls
		if err := p.startListener(ls); err != nil {
			delete(p.servers, port)
			return xerrors.Newf("start listener on port %s: %w", port, err)
		}
		slog.Info("new http listener started", "port", port)
	}
	raw, err := createHandlerForRoute(&route, p.otelEnabled)
	if err != nil {
		return xerrors.Newf("create handler: %w", err)
	}
	r := route
	finalHandler := wrapRouteHandler(&r, raw)

	ls.mu.Lock()
	ls.Routes[host] = &r
	old := ls.handlers.Load().(map[string]http.Handler)
	newMap := make(map[string]http.Handler, len(old)+1)
	for k, v := range old {
		newMap[k] = v
	}
	newMap[host] = finalHandler
	ls.handlers.Store(newMap)
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
	if ok {
		ls.mu.Lock()
		delete(ls.Routes, host)
		old := ls.handlers.Load().(map[string]http.Handler)
		newMap := make(map[string]http.Handler, len(old))
		for k, v := range old {
			if k != host {
				newMap[k] = v
			}
		}
		ls.handlers.Store(newMap)
		ls.mu.Unlock()
		slog.Info("route unregistered", "url", url)
		return
	}

	// Not an HTTP route — stop any raw listeners on this port. A tcp+udp route
	// has both; stopping a non-existent one is a no-op.
	p.stopTCPProxy(port)
	p.stopUDPProxy(port)
	slog.Info("raw route unregistered", "url", url)
}

// wrapRouteHandler applies auth middleware (and API injection for InjectAPI routes)
// once at registration time so the router hot path just calls ServeHTTP.
func wrapRouteHandler(route *ProxyRoute, raw http.Handler) http.Handler {
	h := withAuthForKey(route.Url, raw)
	if route.InjectAPI {
		h = withAPIInject(h)
	}
	return h
}

// withAPIInject intercepts /api/<name> requests and dispatches them to the
// registered API handler, bypassing the auth middleware and the backend proxy.
// Non-/api/ paths and unknown API names fall through to next.
func withAPIInject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			name := strings.TrimPrefix(r.URL.Path, "/api/")
			if h, err := api.Get(name); err == nil {
				h(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
