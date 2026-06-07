package proxy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"reMazarin/storage"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	authStore      *storage.Storage
	authCache      atomic.Value // stores map[string]cachedRoute
	globalSettings atomic.Value // stores storage.Settings

	logChan = make(chan logEntry, 512)
)

type logEntry struct {
	ip, username, route string
}

type cachedRoute struct {
	storage.Route
	groupSet     map[string]struct{} // pre-parsed from AllowedGroups ("1","3",…)
	groupIDs     []int               // same groups as ints, for IP-session group filtering
	allowedAddrs []net.IP            // pre-parsed plain IPs from AllowedIPs
	allowedNets  []*net.IPNet        // pre-parsed CIDR ranges from AllowedIPs
}

// InitAuth initialises the auth subsystem and returns a stop function.
// The stop function must be called after all request handlers have stopped
// (i.e. after cleanShutdown) and before the storage is closed, so that
// buffered log entries are flushed to the DB and the ticker does not run
// cleanup queries on a closed connection.
func InitAuth(ctx context.Context, s *storage.Storage) func() {
	authStore = s
	globalSettings.Store(storage.Settings{SessionDurationHours: 168, RenewOnAccess: true})
	refreshCache()

	logDrained := make(chan struct{})
	go func() {
		defer close(logDrained)
		for e := range logChan {
			authStore.LogAccess(context.Background(), e.ip, e.username, e.route)
		}
	}()

	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				refreshCache()
				bctx := context.Background()
				authStore.CleanupExpiredSessions(bctx)
				authStore.CleanupExpiredInvites(bctx)
				authStore.CleanupOldAccessLog(bctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	return func() {
		close(logChan)
		<-logDrained
	}
}

// RefreshCache forces an immediate reload of the route/auth cache from the database.
func RefreshCache() { refreshCache() }

func refreshCache() {
	routes, err := authStore.GetAllRoutes(context.Background())
	if err != nil {
		slog.Error("auth cache refresh failed", "error", err)
		return
	}
	m := make(map[string]cachedRoute, len(routes))
	for _, r := range routes {
		m[r.Url] = parseCachedRoute(r)
	}
	authCache.Store(m)

	if s, err := authStore.GetSettings(context.Background()); err == nil {
		globalSettings.Store(s)
	}
	slog.Debug("auth cache refreshed", "routes", len(routes))
}

func parseCachedRoute(r storage.Route) cachedRoute {
	cr := cachedRoute{Route: r}
	if r.AllowedGroups != "" {
		cr.groupSet = make(map[string]struct{})
		for _, p := range strings.Split(r.AllowedGroups, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cr.groupSet[p] = struct{}{}
				if id, err := strconv.Atoi(p); err == nil {
					cr.groupIDs = append(cr.groupIDs, id)
				}
			}
		}
	}
	for _, entry := range strings.Split(r.AllowedIPs, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, n, err := net.ParseCIDR(entry); err == nil {
				cr.allowedNets = append(cr.allowedNets, n)
			}
		} else if ip := net.ParseIP(entry); ip != nil {
			cr.allowedAddrs = append(cr.allowedAddrs, ip)
		}
	}
	return cr
}

// withAuthForKey returns a handler pre-bound to rk that enforces access control.
// The closure is created once at route registration — no per-request allocation.
func withAuthForKey(rk string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authStore == nil {
			next.ServeHTTP(w, r)
			return
		}

		m := authCache.Load().(map[string]cachedRoute)
		route, found := m[rk]
		clientIP := extractClientIP(r)

		// Verbose auth tracing. match_ip is the IP actually used for matching;
		// remote_addr and the forwarding headers reveal whether a fronting
		// proxy/tunnel is hiding the real client IP in X-Forwarded-For.
		base := []any{
			"route", rk,
			"method", r.Method,
			"path", r.URL.Path,
			"match_ip", clientIP,
			"remote_addr", r.RemoteAddr,
			"x_forwarded_for", r.Header.Get("X-Forwarded-For"),
			"x_real_ip", r.Header.Get("X-Real-IP"),
			"has_cookie", hasSessionCookie(r),
		}

		if !found {
			http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
			slog.Debug("auth deny: route not in cache", base...)
			logAccess(clientIP, "Unauthorized User", rk)
			return
		}

		base = append(base,
			"ip_auth", route.IPAuth,
			"persistent_login", route.PersistentLogin,
			"allowed_groups", route.AllowedGroups,
			"allowed_ips", route.AllowedIPs,
		)

		// Public route: no restrictions configured.
		if !route.IPAuth && route.AllowedGroups == "" && route.AllowedIPs == "" {
			slog.Debug("auth allow: public route", base...)
			logAccess(clientIP, "", rk)
			RecordRequest(rk)
			next.ServeHTTP(w, r)
			return
		}

		gs := globalSettings.Load().(storage.Settings)

		// IP session auth: the connecting IP must have an active session whose user
		// is in the allowed groups (the lookup enforces both, skipping orphaned and
		// non-matching-user sessions on the same IP). A returned session is authorized.
		if route.IPAuth {
			sg, err := authStore.ValidateSessionByIPInGroups(r.Context(), clientIP, route.groupIDs)
			if err != nil {
				slog.Debug("auth: no authorized ip session for match_ip, falling through",
					append(base, "error", err.Error(), "recent_sessions", authStore.DebugDumpSessions(r.Context(), 10))...)
			} else {
				if gs.RenewOnAccess {
					// Renew every session this user holds on the IP so HTTP and TCP
					// activity keep each other's sessions alive (see ExtendUserSessionsByIP).
					authStore.ExtendUserSessionsByIP(r.Context(), sg.UserID, clientIP, gs.SessionDur())
				}
				slog.Debug("auth allow: ip session", append(base, "user", sg.Username, "session_groups", sg.GroupIDs)...)
				logAccess(clientIP, sg.Username, rk)
				RecordRequest(rk)
				next.ServeHTTP(w, r)
				return
			}
		}

		// Static IP allowlist: matching IP grants access without a session.
		if route.AllowedIPs != "" {
			if ipAllows(route, clientIP) {
				slog.Debug("auth allow: ip allowlist", base...)
				logAccess(clientIP, "", rk)
				RecordRequest(rk)
				next.ServeHTTP(w, r)
				return
			}
			slog.Debug("auth: match_ip not in allowlist, falling through", base...)
		}

		// Cookie (persistent-login) auth — an independent alternative to IP session
		// auth. A route that does not enable persistent login does not accept cookie
		// auth at all: even a valid session cookie is ignored and the request denied
		// (the IP checks above were the only way in). The cookie is never touched.
		if !route.PersistentLogin {
			http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
			slog.Debug("auth deny: persistent-login (cookie) auth disabled for route", base...)
			logAccess(clientIP, "Unauthorized User", rk)
			RecordRequest(rk)
			return
		}

		// Cookie-based group auth.
		if len(route.groupSet) == 0 {
			http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
			slog.Debug("auth deny: no allowed groups and ip checks failed", base...)
			logAccess(clientIP, "Unauthorized User", rk)
			RecordRequest(rk)
			return
		}

		c, err := r.Cookie("session")
		if err != nil {
			http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
			slog.Debug("auth deny: no session cookie", base...)
			logAccess(clientIP, "Unauthorized User", rk)
			RecordRequest(rk)
			return
		}
		sg, err := authStore.ValidateSessionAndGroups(r.Context(), c.Value)
		if err != nil {
			http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
			slog.Debug("auth deny: invalid/expired session cookie", append(base, "error", err.Error())...)
			logAccess(clientIP, "Unauthorized User", rk)
			RecordRequest(rk)
			return
		}
		if !groupsAllow(route.groupSet, sg.GroupIDs) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			slog.Debug("auth deny: cookie user not in allowed group", append(base, "user", sg.Username, "session_groups", sg.GroupIDs)...)
			logAccess(clientIP, "Unauthorized User", rk)
			RecordRequest(rk)
			return
		}
		if gs.RenewOnAccess {
			authStore.ExtendSession(r.Context(), c.Value, gs.SessionDur())
		}
		// The cookie itself is not touched here: its lifetime is set once at login
		// (persistent by default) and the DB session — kept alive by access,
		// including TCP — is the authority on validity. IP auth and cookie auth are
		// independent; neither path rewrites the other's cookie.
		slog.Debug("auth allow: cookie session", append(base, "user", sg.Username)...)
		logAccess(clientIP, sg.Username, rk)
		RecordRequest(rk)
		next.ServeHTTP(w, r)
	})
}

func logAccess(ip, username, route string) {
	select {
	case logChan <- logEntry{ip, username, route}:
	default:
		// Drop rather than stall the request handler if the log queue is full.
	}
}

// groupsAllow returns true if any of the user's group IDs appear in the pre-parsed set.
func groupsAllow(groupSet map[string]struct{}, groupIDs []int) bool {
	for _, id := range groupIDs {
		if _, ok := groupSet[strconv.Itoa(id)]; ok {
			return true
		}
	}
	return false
}

// hasSessionCookie reports whether the request carries a session cookie.
// Used only for auth debug logging.
func hasSessionCookie(r *http.Request) bool {
	_, err := r.Cookie("session")
	return err == nil
}

// extractClientIP returns the IP address part of r.RemoteAddr.
func extractClientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// ipAllows returns true if clientIP matches any pre-parsed entry in cr.
func ipAllows(cr cachedRoute, clientIP string) bool {
	addr := net.ParseIP(clientIP)
	if addr == nil {
		return false
	}
	for _, ip := range cr.allowedAddrs {
		if ip.Equal(addr) {
			return true
		}
	}
	for _, n := range cr.allowedNets {
		if n.Contains(addr) {
			return true
		}
	}
	return false
}
