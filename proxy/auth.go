package proxy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"reMazarin/storage"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	authStore *storage.Storage

	cacheMu sync.RWMutex
	cache   map[string]storage.Route // "host:port" → route
)

func InitAuth(s *storage.Storage) {
	authStore = s
	cache = make(map[string]storage.Route)
	refreshCache()
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			refreshCache()
			ctx := context.Background()
			authStore.CleanupExpiredSessions(ctx)
			authStore.CleanupExpiredInvites(ctx)
		}
	}()
}

// RefreshCache forces an immediate reload of the route/auth cache from the database.
// Call this after any route access-control update that should take effect right away.
func RefreshCache() { refreshCache() }

func refreshCache() {
	routes, err := authStore.GetAllRoutes(context.Background())
	if err != nil {
		slog.Error("auth cache refresh failed", "error", err)
		return
	}
	m := make(map[string]storage.Route, len(routes))
	for _, r := range routes {
		m[r.Url] = r
	}
	cacheMu.Lock()
	cache = m
	cacheMu.Unlock()
	slog.Debug("auth cache refreshed", "routes", len(routes))
}

// withAuth wraps a handler with session/group validation based on the route's
// allowed_groups. Routes with empty allowed_groups are public.
func withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authStore == nil {
			next.ServeHTTP(w, r)
			return
		}

		cacheMu.RLock()
		route, found := cache[routeKey(r)]
		cacheMu.RUnlock()

		if !found || route.AllowedGroups == "" {
			next.ServeHTTP(w, r)
			return
		}

		c, err := r.Cookie("session")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		sess, err := authStore.ValidateSession(r.Context(), c.Value)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		groups, err := authStore.GetUserGroups(r.Context(), sess.UserID)
		if err != nil || !groupsAllow(route.AllowedGroups, groups) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if route.RenewOnAccess {
			authStore.ExtendSession(r.Context(), c.Value, 7*24*time.Hour)
		}
		next.ServeHTTP(w, r)
	})
}

// routeKey normalises r.Host to "host:port" to match stored route URLs.
func routeKey(r *http.Request) string {
	h := strings.ToLower(r.Host)
	if _, _, err := net.SplitHostPort(h); err == nil {
		return h
	}
	if r.TLS != nil {
		return h + ":443"
	}
	return h + ":80"
}

// groupsAllow returns true if any of the user's groups appear in the
// comma-separated allowed group-ID list.
func groupsAllow(allowed string, groups []storage.Group) bool {
	set := make(map[string]bool)
	for _, part := range strings.Split(allowed, ",") {
		if p := strings.TrimSpace(part); p != "" {
			set[p] = true
		}
	}
	for _, g := range groups {
		if set[strconv.Itoa(g.ID)] {
			return true
		}
	}
	return false
}
