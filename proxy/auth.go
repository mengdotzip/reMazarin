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

// withAuth wraps a handler with session/group and IP-based validation.
// Routes with empty allowed_groups and empty allowed_ips are public.
func withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authStore == nil {
			next.ServeHTTP(w, r)
			return
		}

		cacheMu.RLock()
		route, found := cache[routeKey(r)]
		cacheMu.RUnlock()

		rk := routeKey(r)
		serve := func() {
			RecordRequest(rk)
			next.ServeHTTP(w, r)
		}

		// Public: no restrictions at all.
		if !found || (!route.IPAuth && route.AllowedGroups == "" && route.AllowedIPs == "") {
			serve()
			return
		}

		clientIP := extractClientIP(r)

		// IP session auth: connecting IP has an active session → grant access.
		// If allowed_groups is also set, the session user must be in one of those groups.
		if route.IPAuth {
			if sess, err := authStore.ValidateSessionByIP(r.Context(), clientIP); err == nil {
				authorized := route.AllowedGroups == ""
				if !authorized {
					if groups, err := authStore.GetUserGroups(r.Context(), sess.UserID); err == nil {
						authorized = groupsAllow(route.AllowedGroups, groups)
					}
				}
				if authorized {
					if route.RenewOnAccess {
						authStore.ExtendSessionByID(r.Context(), sess.ID, routeSessionDur(route))
					}
					serve()
					return
				}
				// Session found but user not in required group — fall through to other methods.
			}
			// No active session for this IP — fall through.
		}

		// Static IP allowlist: matching IP grants access without a session.
		if route.AllowedIPs != "" && ipAllows(route.AllowedIPs, clientIP) {
			serve()
			return
		}

		// Cookie-based group auth.
		if route.AllowedGroups == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
			authStore.ExtendSession(r.Context(), c.Value, routeSessionDur(route))
		}
		serve()
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

// extractClientIP returns the IP address part of r.RemoteAddr.
func extractClientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// routeSessionDur returns the session renewal duration for the route.
// Falls back to 7 days when session_duration is not set.
func routeSessionDur(route storage.Route) time.Duration {
	if route.SessionDuration > 0 {
		return time.Duration(route.SessionDuration) * time.Hour
	}
	return 7 * 24 * time.Hour
}

// ipAllows returns true if clientIP matches any entry in the comma-separated
// allowedIPs list (plain IPs or CIDR ranges).
func ipAllows(allowedIPs, clientIP string) bool {
	addr := net.ParseIP(clientIP)
	if addr == nil {
		return false
	}
	for _, entry := range strings.Split(allowedIPs, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err == nil && network.Contains(addr) {
				return true
			}
		} else {
			if ip := net.ParseIP(entry); ip != nil && ip.Equal(addr) {
				return true
			}
		}
	}
	return false
}
