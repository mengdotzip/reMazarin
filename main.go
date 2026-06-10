package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"reMazarin/api"
	"reMazarin/proxy"
	"reMazarin/storage"
	"sync"
	"syscall"
	"time"

	"github.com/mdobak/go-xerrors"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
)

const version = "0.1.1"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := setupLogging()
	slog.SetDefault(logger)

	logger.Info("starting reMazarin", "version", version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig("config.toml")
	if err != nil {
		return xerrors.Newf("load config: %w", err)
	}

	slog.Info("config loaded",
		"web_enabled", cfg.Web.Enabled,
		"admin_enabled", cfg.Admin.Enabled,
		"routes_count", len(cfg.Routes),
	)

	if cfg.Otel.Enabled {
		otelShutdown, err := setupOTelSDK(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() {
			err = xerrors.Newf("otel shutdown: %w", errors.Join(err, otelShutdown(context.Background())))
		}()
		if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Duration(cfg.Otel.RuntimeInterval) * time.Second)); err != nil {
			slog.Error("otel runtime start failed", "error", err)
		}
	}

	if err := api.InitBuiltin(); err != nil {
		return xerrors.Newf("init builtin api: %w", err)
	}
	if err := api.InitApi(); err != nil {
		return xerrors.Newf("init api: %w", err)
	}

	store, err := storage.New(cfg.Database)
	if err != nil {
		return xerrors.Newf("open storage: %w", err)
	}
	defer store.Close()

	api.SetStore(store)
	scheme := "http"
	if cfg.Web.Tls {
		scheme = "https"
	}
	api.SetAuthURL(scheme + "://" + cfg.Web.Url)
	// stopAuth must be deferred before store.Close so that the log drainer
	// flushes buffered entries while the DB is still open (LIFO defer order).
	stopAuth := proxy.InitAuth(ctx, store)
	defer stopAuth()

	configRoutes := make([]storage.ConfigRoute, len(cfg.Routes))
	for i, r := range cfg.Routes {
		configRoutes[i] = storage.ConfigRoute{
			Url: r.Url, Target: r.Target, Type: r.Type,
			Tls: r.Tls, Cert: r.Cert, Key: r.Key,
		}
	}
	if err := store.SyncRoutes(configRoutes); err != nil {
		return xerrors.Newf("sync routes: %w", err)
	}

	// Protect the admin route with the admin group on first creation.
	// EnsureRouteGroup is a no-op if allowed_groups has already been configured.
	if cfg.Admin.Enabled {
		if err := store.EnsureRouteGroup(context.Background(), cfg.Admin.Url, "admin"); err != nil {
			slog.Warn("could not protect admin route", "error", err)
		}
	}

	// Refresh the auth cache now that routes are synced and protected.
	// (InitAuth ran before SyncRoutes so the initial cache load was empty.)
	proxy.RefreshCache()
	api.OnRouteUpdate = proxy.RefreshCache
	api.RouteStats = proxy.GetRouteStats
	api.EventStats = proxy.GetEventStats
	api.RecentEvents = func() any { return proxy.GetRecentEvents() }
	api.ActiveBans = proxy.GetActiveBans
	api.BanIP = proxy.BanIP
	api.UnbanIP = proxy.UnbanIP
	api.DefaultCert = cfg.Web.Cert
	api.DefaultKey = cfg.Web.Key

	allRoutes, err := store.GetAllRoutes(context.Background())
	if err != nil {
		return xerrors.Newf("get routes: %w", err)
	}
	proxyRoutes := make([]proxy.ProxyRoute, len(allRoutes))
	for i, r := range allRoutes {
		proxyRoutes[i] = proxy.ProxyRoute{
			Url: r.Url, Target: r.Target, Type: r.Type,
			Tls: r.Tls, Cert: r.Cert, Key: r.Key,
			InjectAPI: r.Url == cfg.Web.Url || r.Url == cfg.Admin.Url,
		}
	}

	var wg sync.WaitGroup
	p := proxy.Proxy{Proxies: proxyRoutes, Wg: &wg}

	// Wire dynamic route callbacks after p is initialised.
	api.OnRouteRegister = func(url, target, routeType string, tls bool, cert, key string) error {
		return p.RegisterRoute(proxy.ProxyRoute{Url: url, Target: target, Type: routeType, Tls: tls, Cert: cert, Key: key})
	}
	api.OnRouteDelete = func(url string) { p.UnregisterRoute(url) }

	api.OnRouteValidate = p.ValidateRoute

	if err := p.StartProxy(ctx, cfg.Otel.Enabled); err != nil {
		return xerrors.Newf("start proxy: %w", err)
	}

	return cleanShutdown(ctx, p.Wg, p.ErrChan, p.ShutdownHTTP)
}

func cleanShutdown(ctx context.Context, wg *sync.WaitGroup, errChan chan error, shutdownHTTP func(context.Context)) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	var shutdownErr error
	select {
	case <-ctx.Done():
		slog.Info("context cancelled, shutting down")
	case err := <-errChan:
		slog.Error("listener error, shutting down", "error", err)
		shutdownErr = err
	case <-done:
		slog.Info("all goroutines finished")
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownHTTP(shutdownCtx)

	select {
	case <-done:
		slog.Info("all servers stopped gracefully")
	case <-time.After(6 * time.Second):
		slog.Warn("shutdown timeout")
	}
	return shutdownErr
}
