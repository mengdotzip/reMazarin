package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"reMazarin/proxy"
	"reMazarin/storage"
	"sync"
	"syscall"
	"time"

	"github.com/mdobak/go-xerrors"
)

const version = "0.0.1"

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

	// Load config
	cfg, err := loadConfig("config.toml")
	if err != nil {
		return xerrors.Newf("load config: %w", err)
	}

	slog.Info("config loaded",
		"web_enabled", cfg.Web.Enabled,
		"database", cfg.Database,
		"admin_enabled", cfg.Admin.Enabled,
		"routes_count", len(cfg.Routes),
	)

	var proxyRoutes []proxy.ProxyRoute

	if cfg.Admin.Enabled || cfg.Web.Enabled {
		// Initialize storage
		store, err := storage.New(cfg.Database)
		if err != nil {
			return xerrors.Newf("initialize storage: %w", err)
		}
		defer store.Close()

		// Sync toml to sqlite
		configRoutes := make([]storage.ConfigRoute, len(cfg.Routes))
		for i, r := range cfg.Routes {
			configRoutes[i] = storage.ConfigRoute{
				Url:    r.Url,
				Target: r.Target,
				Tls:    r.Tls,
				Cert:   r.Cert,
				Key:    r.Key,
			}
		}

		if err := store.SyncRoutes(configRoutes); err != nil {
			return xerrors.Newf("sync routes: %w", err)
		}

		// Get all routes
		ctx := context.Background()
		allRoutes, err := store.GetAllRoutes(ctx)
		if err != nil {
			return xerrors.Newf("get routes: %w", err)
		}

		proxyRoutes = make([]proxy.ProxyRoute, len(allRoutes))
		for i, r := range allRoutes {
			proxyRoutes[i] = proxy.ProxyRoute{
				Url:    r.Url,
				Target: r.Target,
				Tls:    r.Tls,
				Cert:   r.Cert,
				Key:    r.Key,
			}
		}
	} else {
		proxyRoutes = make([]proxy.ProxyRoute, len(cfg.Routes))
		for i, r := range cfg.Routes {
			proxyRoutes[i] = proxy.ProxyRoute{
				Url:    r.Url,
				Target: r.Target,
				Tls:    r.Tls,
				Cert:   r.Cert,
				Key:    r.Key,
			}
		}
	}

	// Start proxies
	var wg sync.WaitGroup
	proxy := proxy.Proxy{
		Proxies: proxyRoutes,
		Wg:      &wg,
	}

	servers, err := proxy.StartProxy()
	if err != nil {
		return xerrors.Newf("start proxy: %w", err)
	}

	// Shutdown wait
	cleanShutdown(ctx, proxy.Wg, proxy.ErrChan, servers)

	return nil
}

func cleanShutdown(ctx context.Context, wg *sync.WaitGroup, errChan chan error, servers []*http.Server) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	var shutdownErr error

	select {
	case <-ctx.Done():
		slog.Info("context cancelled")
	case err := <-errChan:
		slog.Error("listener failure detected, initiating shutdown", "error", err)
		shutdownErr = err
	case <-done:
		slog.Info("all goroutines finished unexpectedly")
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, serve := range servers {
		serve.Shutdown(shutdownCtx)
	}
	shutdownTimeout := 6 * time.Second

	select {
	case <-done:
		slog.Info("all servers stopped gracefully")
	case <-time.After(shutdownTimeout):
		slog.Warn("shutdown timeout reached, forcing exit")
	}
	return shutdownErr
}
