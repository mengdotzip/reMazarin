package proxy

import (
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/mdobak/go-xerrors"
)

func createReverseProxy(route *ProxyRoute) (*httputil.ReverseProxy, error) {
	targetAddr := route.Target

	if !strings.HasPrefix(targetAddr, "http://") && !strings.HasPrefix(targetAddr, "https://") {
		targetAddr = "http://" + targetAddr
	}

	target, err := url.Parse(targetAddr)
	if err != nil {
		return nil, xerrors.Newf("invalid target URL %s: %w", targetAddr, err)
	}

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := &http.Transport{ // WARNING: Testing what config we need and if we need to add this to the config.toml
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}

	// Allow insecure HTTPS (not used yet)
	if strings.HasPrefix(targetAddr, "https://") {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	proxy.Transport = transport

	// Customize Director
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Set("X-Forwarded-Host", req.Host)
		req.Header.Set("X-Origin-Host", target.Host)
		req.Header.Set("X-Proxy", "reMazarin")
	}

	// Error handler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Error("proxy error",
			"target", targetAddr,
			"path", r.URL.Path,
			"error", err,
		)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	return proxy, nil
}
