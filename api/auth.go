package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"reMazarin/storage"
	"strconv"
	"strings"
	"time"
)

// OnRouteUpdate is called after a route's access control is changed so the
// proxy cache can be refreshed immediately. Set this in main.go.
var OnRouteUpdate func()

// OnRouteRegister registers (or re-registers) a route in the live proxy.
var OnRouteRegister func(url, target, routeType string, tls bool, cert, key string) error

// OnRouteValidate is called before persisting a new route to check for
// conflicts with the live proxy state (port conflicts, invalid format).
var OnRouteValidate func(url, routeType string) error

// DefaultCert and DefaultKey are the fallback TLS certificate paths used when
// creating UI routes with TLS enabled. Set from the web host config in main.go.
var DefaultCert, DefaultKey string

// OnRouteDelete removes a route from the live proxy.
var OnRouteDelete func(url string)

var store *storage.Storage
var authURL string

func SetStore(s *storage.Storage) { store = s }
func SetAuthURL(u string)          { authURL = u }

func HandleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ok(w, map[string]string{"auth_url": authURL})
}

const (
	sessionCookie    = "session"
	defaultSessionDur = 7 * 24 * time.Hour
	// cookieMaxAge is how long the browser retains the session cookie. It is
	// deliberately decoupled from (and much longer than) the session duration:
	// the DB session — which TCP/IP activity keeps alive — is the authority on
	// expiry, re-checked on every request (expires_at > now). Tying the cookie to
	// session_duration made an idle browser silently drop the cookie after the
	// session length even while TCP kept the underlying session alive, forcing a
	// needless re-login. 400 days is the practical browser cap for persistent cookies.
	cookieMaxAge = 400 * 24 * time.Hour
	// maxPortRange caps how many ports a single port-range route may span. Each
	// port becomes its own listener (an http.Server or a TCP goroutine), so an
	// unbounded range could exhaust file descriptors / sockets.
	maxPortRange = 256
)

// ---- helpers ----------------------------------------------------------------

func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decode(r *http.Request, v any) bool {
	return json.NewDecoder(r.Body).Decode(v) == nil
}

// rootDomain extracts the registrable domain for cross-subdomain cookie scope.
// "admin.meng.zip:8081" → "meng.zip", "localhost:8080" → "".
func rootDomain(hostHeader string) string {
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// setSession issues the login cookie. It is always a persistent (long-lived)
// cookie so it survives browser restarts/idle; the DB session — kept alive by
// access, including TCP — is the authority on validity. Whether a given route
// honours this cookie is a separate, per-route decision (persistent_login),
// enforced in the proxy; that gate never rewrites the cookie itself.
func setSession(w http.ResponseWriter, r *http.Request, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		Domain:   rootDomain(r.Host),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(cookieMaxAge.Seconds()),
	})
}

func clearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		Domain:   rootDomain(r.Host),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func sessionFromRequest(r *http.Request) (*storage.Session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, err
	}
	return store.ValidateSession(r.Context(), c.Value)
}

// requireAdmin returns the session if the caller is logged in as an admin,
// otherwise writes an error response and returns nil.
func requireAdmin(w http.ResponseWriter, r *http.Request) *storage.Session {
	sess, err := sessionFromRequest(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "not authenticated")
		return nil
	}
	ok2, err := store.UserInGroup(r.Context(), sess.UserID, "admin")
	if err != nil || !ok2 {
		fail(w, http.StatusForbidden, "admin required")
		return nil
	}
	return sess
}

// ---- auth endpoints ---------------------------------------------------------

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(r, &body) {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	clientIP, _, _ := net.SplitHostPort(r.RemoteAddr)
	user, err := store.Authenticate(r.Context(), body.Username, body.Password)
	if err != nil {
		slog.Warn("login failed", "username", body.Username)
		store.LogAuthFailure(r.Context(), clientIP, body.Username)
		fail(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	settings, _ := store.GetSettings(r.Context())
	dur := settings.SessionDur()
	if dur <= 0 {
		dur = defaultSessionDur
	}
	tok, err := store.CreateSession(r.Context(), user.ID, dur, clientIP)
	if err != nil {
		fail(w, http.StatusInternalServerError, "session error")
		return
	}
	setSession(w, r, tok)
	groups, _ := store.GetUserGroups(r.Context(), user.ID)
	ok(w, map[string]any{"user": user, "groups": groups})
}

func HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		store.DeleteSession(r.Context(), c.Value)
	}
	clearSession(w, r)
	ok(w, map[string]bool{"ok": true})
}

func HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Invite   string `json:"invite"`
	}
	if !decode(r, &body) || body.Username == "" || body.Password == "" || body.Invite == "" {
		fail(w, http.StatusBadRequest, "username, password and invite are required")
		return
	}
	if _, err := store.UseInvite(r.Context(), body.Invite); err != nil {
		fail(w, http.StatusBadRequest, "invalid or expired invite")
		return
	}
	user, err := store.CreateUser(r.Context(), body.Username, body.Password)
	if err != nil {
		fail(w, http.StatusConflict, "username already taken")
		return
	}
	ok(w, map[string]any{"user": user})
}

func HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sess, err := sessionFromRequest(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	user, err := store.GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		fail(w, http.StatusNotFound, "user not found")
		return
	}
	groups, _ := store.GetUserGroups(r.Context(), user.ID)
	ok(w, map[string]any{"user": user, "groups": groups})
}

// ---- auth: accessible routes ------------------------------------------------

func HandleUserRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sess, err := sessionFromRequest(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	groups, err := store.GetUserGroups(r.Context(), sess.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	groupIDs := make([]int, len(groups))
	for i, g := range groups {
		groupIDs[i] = g.ID
	}
	routes, err := store.GetAllRoutes(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	type routeInfo struct {
		URL             string `json:"url"`
		Tls             bool   `json:"tls"`
		SessionDuration int    `json:"session_duration"`
		RenewOnAccess   bool   `json:"renew_on_access"`
	}
	// Session duration and renewal are global; report the same values the proxy
	// actually enforces so the auth page display matches reality.
	settings, _ := store.GetSettings(r.Context())
	durHours := int(settings.SessionDur().Hours())
	// Exclude the proxy/auth page itself by comparing hostnames.
	selfHost := strings.SplitN(r.Host, ":", 2)[0]
	var accessible []routeInfo
	for _, rt := range routes {
		// Skip raw (tcp/udp) routes and internal API handlers — not browser-navigable.
		if isRawType(rt.Type) || rt.Type == "api" {
			continue
		}
		// Skip this proxy/auth page.
		if strings.SplitN(rt.Url, ":", 2)[0] == selfHost {
			continue
		}
		if storage.RouteAllows(rt.AllowedGroups, groupIDs) {
			accessible = append(accessible, routeInfo{rt.Url, rt.Tls, durHours, settings.RenewOnAccess})
		}
	}
	if accessible == nil {
		accessible = []routeInfo{}
	}
	ok(w, map[string]any{"routes": accessible})
}

// HandleExtendSession extends the current session using a specific route's
// session_duration. The caller must have access to the named route.
// POST /api/auth/extend?url=<route-url>
func HandleExtendSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sess, err := sessionFromRequest(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	routeURL := r.URL.Query().Get("url")
	if routeURL == "" {
		fail(w, http.StatusBadRequest, "url required")
		return
	}
	rt, err := store.GetRouteByUrl(r.Context(), routeURL)
	if err != nil {
		fail(w, http.StatusNotFound, "route not found")
		return
	}
	groups, err := store.GetUserGroups(r.Context(), sess.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	groupIDs := make([]int, len(groups))
	for i, g := range groups {
		groupIDs[i] = g.ID
	}
	if !storage.RouteAllows(rt.AllowedGroups, groupIDs) {
		fail(w, http.StatusForbidden, "access denied")
		return
	}
	settings, _ := store.GetSettings(r.Context())
	dur := settings.SessionDur()
	store.ExtendSessionByID(r.Context(), sess.ID, dur)
	// Roll the cookie's lifetime forward alongside the DB session.
	if c, err := r.Cookie(sessionCookie); err == nil {
		setSession(w, r, c.Value)
	}
	ok(w, map[string]any{"expires_at": time.Now().Add(dur)})
}

// ---- admin: users -----------------------------------------------------------

func HandleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := store.GetAllUsers(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		type userRow struct {
			storage.User
			Groups []storage.Group `json:"groups"`
		}
		rows := make([]userRow, 0, len(users))
		for _, u := range users {
			groups, _ := store.GetUserGroups(r.Context(), u.ID)
			if groups == nil {
				groups = []storage.Group{}
			}
			rows = append(rows, userRow{u, groups})
		}
		ok(w, map[string]any{"users": rows})

	case http.MethodDelete:
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := store.DeleteUser(r.Context(), id); err != nil {
			fail(w, http.StatusNotFound, "user not found")
			return
		}
		ok(w, map[string]bool{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- admin: user-group membership ------------------------------------------

func HandleAdminUserGroups(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			UserID  int `json:"user_id"`
			GroupID int `json:"group_id"`
		}
		if !decode(r, &body) {
			fail(w, http.StatusBadRequest, "invalid request")
			return
		}
		if err := store.AddUserToGroup(r.Context(), body.UserID, body.GroupID); err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		ok(w, map[string]bool{"ok": true})

	case http.MethodDelete:
		uid, err1 := strconv.Atoi(r.URL.Query().Get("user_id"))
		gid, err2 := strconv.Atoi(r.URL.Query().Get("group_id"))
		if err1 != nil || err2 != nil {
			fail(w, http.StatusBadRequest, "invalid ids")
			return
		}
		if err := store.RemoveUserFromGroup(r.Context(), uid, gid); err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		ok(w, map[string]bool{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- admin: groups ----------------------------------------------------------

func HandleAdminGroups(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		groups, err := store.GetAllGroups(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if groups == nil {
			groups = []storage.Group{}
		}
		ok(w, map[string]any{"groups": groups})

	case http.MethodPost:
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decode(r, &body) || body.Name == "" {
			fail(w, http.StatusBadRequest, "name required")
			return
		}
		g, err := store.CreateGroup(r.Context(), body.Name, body.Description)
		if err != nil {
			fail(w, http.StatusConflict, "group name taken")
			return
		}
		ok(w, map[string]any{"group": g})

	case http.MethodDelete:
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := store.DeleteGroup(r.Context(), id); err != nil {
			if errors.Is(err, storage.ErrGroupProtected) {
				fail(w, http.StatusBadRequest, "cannot delete the admin group")
				return
			}
			fail(w, http.StatusNotFound, "group not found")
			return
		}
		ok(w, map[string]bool{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- admin: invites ---------------------------------------------------------

func HandleAdminInvites(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		invites, err := store.GetAllInvites(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if invites == nil {
			invites = []storage.Invite{}
		}
		ok(w, map[string]any{"invites": invites})

	case http.MethodPost:
		var body struct {
			Description string `json:"description"`
			Hours       int    `json:"hours"`
		}
		decode(r, &body)
		if body.Hours <= 0 {
			body.Hours = 24
		}
		code, inv, err := store.CreateInvite(r.Context(), body.Description, time.Duration(body.Hours)*time.Hour)
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		ok(w, map[string]any{"invite": inv, "code": code})

	case http.MethodDelete:
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := store.DeleteInvite(r.Context(), id); err != nil {
			fail(w, http.StatusNotFound, "invite not found")
			return
		}
		ok(w, map[string]bool{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- admin: routes ----------------------------------------------------------

func HandleAdminRoutes(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		routes, err := store.GetAllRoutes(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if routes == nil {
			routes = []storage.Route{}
		}
		ok(w, map[string]any{"routes": routes})

	case http.MethodPost:
		var body struct {
			URL      string `json:"url"`
			Target   string `json:"target"`
			Type     string `json:"type"`
			Tls      bool   `json:"tls"`
			RangeEnd int    `json:"range_end"` // last port of a port range; 0 = single route
			Offset   bool   `json:"offset"`    // walk the target port alongside the listen port
		}
		if !decode(r, &body) || body.URL == "" || body.Target == "" {
			fail(w, http.StatusBadRequest, "url and target required")
			return
		}
		if body.Type == "" {
			body.Type = "proxy"
		}
		// Raw (tcp/udp) routes do not terminate TLS — ignore the flag if set.
		if isRawType(body.Type) {
			body.Tls = false
		}
		cert, key := "", ""
		if body.Tls {
			cert, key = DefaultCert, DefaultKey
		}

		host, startPort, err := splitHostPortNum(body.URL)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid url: expected host:port")
			return
		}
		// Port-range route: expand into one row + listener per port, all sharing a
		// range_group so the admin UI can manage them as a single logical route.
		if body.RangeEnd > startPort {
			createRouteRange(w, r, body.Type, host, startPort, body.RangeEnd, body.Target, body.Offset, body.Tls, cert, key)
			return
		}

		// Validate against the live proxy state before touching the DB.
		if OnRouteValidate != nil {
			if err := OnRouteValidate(body.URL, body.Type); err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		route, err := store.CreateRoute(r.Context(), body.URL, body.Target, body.Type, body.Tls, cert, key, "")
		if err != nil {
			fail(w, http.StatusConflict, "url already in use")
			return
		}
		var regErr string
		if OnRouteRegister != nil {
			if err := OnRouteRegister(body.URL, body.Target, body.Type, body.Tls, cert, key); err != nil {
				slog.Warn("route saved but not live", "url", body.URL, "error", err)
				regErr = err.Error()
			}
		}
		if OnRouteUpdate != nil {
			OnRouteUpdate()
		}
		ok(w, map[string]any{"route": route, "warning": regErr})

	case http.MethodPut:
		var body struct {
			AllowedGroups   string `json:"allowed_groups"`
			AllowedIPs      string `json:"allowed_ips"`
			IPAuth          bool   `json:"ip_auth"`
			PersistentLogin bool   `json:"persistent_login"`
			RequireLogin    bool   `json:"require_login"`
			Target          string `json:"target"`
		}
		// A port-range route is edited as a whole via ?group=. Access-control
		// fields are identical across every port; target edits are not supported
		// for ranges (the per-port offset makes a single target ambiguous).
		if group := r.URL.Query().Get("group"); group != "" {
			if !decode(r, &body) {
				fail(w, http.StatusBadRequest, "invalid request")
				return
			}
			if body.AllowedGroups != "" {
				if rt, err := store.GetRouteByGroup(r.Context(), group); err == nil && isRawType(rt.Type) {
					body.IPAuth = true
				}
			}
			if _, err := store.UpdateRouteAccessByGroup(r.Context(), group, body.AllowedGroups, body.AllowedIPs, body.IPAuth, body.PersistentLogin, body.RequireLogin); err != nil {
				fail(w, http.StatusNotFound, "range group not found")
				return
			}
			if OnRouteUpdate != nil {
				OnRouteUpdate()
			}
			ok(w, map[string]bool{"ok": true})
			return
		}
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid id")
			return
		}
		if !decode(r, &body) {
			fail(w, http.StatusBadRequest, "invalid request")
			return
		}
		// Raw (tcp/udp) routes have no cookie/HTTP login, so IP session auth is the
		// only way to enforce group membership. Selecting allowed groups implies
		// ip_auth — persist it so stored state and admin UI reflect what is enforced.
		if body.AllowedGroups != "" {
			if rt, err := store.GetRouteByID(r.Context(), id); err == nil && isRawType(rt.Type) {
				body.IPAuth = true
			}
		}
		if err := store.UpdateRouteAccess(r.Context(), id, body.AllowedGroups, body.AllowedIPs, body.IPAuth, body.PersistentLogin, body.RequireLogin); err != nil {
			fail(w, http.StatusNotFound, "route not found")
			return
		}
		// Update backend target for UI-sourced routes only.
		if body.Target != "" && OnRouteRegister != nil {
			if rt, err := store.GetRouteByID(r.Context(), id); err == nil && rt.Source == "ui" {
				if err := store.UpdateRouteEndpoint(r.Context(), id, body.Target); err == nil {
					OnRouteRegister(rt.Url, body.Target, rt.Type, rt.Tls, rt.Cert, rt.Key)
				}
			}
		}
		if OnRouteUpdate != nil {
			OnRouteUpdate()
		}
		ok(w, map[string]bool{"ok": true})

	case http.MethodDelete:
		// Deleting a port-range route removes every port in the group at once.
		if group := r.URL.Query().Get("group"); group != "" {
			urls, err := store.DeleteRouteGroup(r.Context(), group)
			if err != nil {
				fail(w, http.StatusBadRequest, "range group not found")
				return
			}
			if OnRouteDelete != nil {
				for _, url := range urls {
					OnRouteDelete(url)
				}
			}
			if OnRouteUpdate != nil {
				OnRouteUpdate()
			}
			ok(w, map[string]bool{"ok": true})
			return
		}
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid id")
			return
		}
		url, err := store.DeleteRoute(r.Context(), id)
		if err != nil {
			fail(w, http.StatusBadRequest, "route not found or config routes cannot be deleted")
			return
		}
		if OnRouteDelete != nil {
			OnRouteDelete(url)
		}
		if OnRouteUpdate != nil {
			OnRouteUpdate()
		}
		ok(w, map[string]bool{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// isRawType reports whether a route type is a raw (connectionless/non-HTTP)
// protocol: tcp, udp, or the combined tcp+udp. These have no cookie/HTTP login,
// so they are gated by client IP only.
func isRawType(t string) bool { return t == "tcp" || t == "udp" || t == "tcp+udp" }

// splitHostPortNum splits a "host:port" string and parses the numeric port.
func splitHostPortNum(hostPort string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, err
	}
	return h, n, nil
}

// createRouteRange expands a port range into one route + listener per port, all
// sharing a generated range_group. Every port is validated up front; on a
// mid-loop failure the already-created ports are rolled back so no partial range
// is left behind. With offset set, each port forwards to a target port walked
// from the base target port; otherwise every port forwards to the same target.
func createRouteRange(w http.ResponseWriter, r *http.Request, routeType, host string, startPort, endPort int, target string, offset, tls bool, cert, key string) {
	span := endPort - startPort + 1
	if span > maxPortRange {
		fail(w, http.StatusBadRequest, "port range too large (max "+strconv.Itoa(maxPortRange)+" ports)")
		return
	}

	var targetHost string
	var targetPort int
	if offset {
		var err error
		targetHost, targetPort, err = splitHostPortNum(target)
		if err != nil {
			fail(w, http.StatusBadRequest, "offset target must be host:port")
			return
		}
	}

	// Validate every port before creating anything so a conflict aborts cleanly.
	if OnRouteValidate != nil {
		for p := startPort; p <= endPort; p++ {
			u := net.JoinHostPort(host, strconv.Itoa(p))
			if err := OnRouteValidate(u, routeType); err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}

	rangeGroup := storage.NewRangeGroupID()
	var regErr string
	for p := startPort; p <= endPort; p++ {
		u := net.JoinHostPort(host, strconv.Itoa(p))
		tgt := target
		if offset {
			tgt = net.JoinHostPort(targetHost, strconv.Itoa(targetPort+(p-startPort)))
		}
		if _, err := store.CreateRoute(r.Context(), u, tgt, routeType, tls, cert, key, rangeGroup); err != nil {
			// Roll back ports already created so we never leave a partial range.
			if urls, derr := store.DeleteRouteGroup(r.Context(), rangeGroup); derr == nil && OnRouteDelete != nil {
				for _, du := range urls {
					OnRouteDelete(du)
				}
			}
			fail(w, http.StatusConflict, "port "+strconv.Itoa(p)+" already in use")
			return
		}
		if OnRouteRegister != nil {
			if err := OnRouteRegister(u, tgt, routeType, tls, cert, key); err != nil {
				slog.Warn("range route saved but not live", "url", u, "error", err)
				regErr = err.Error()
			}
		}
	}
	if OnRouteUpdate != nil {
		OnRouteUpdate()
	}
	ok(w, map[string]any{"range_group": rangeGroup, "count": span, "warning": regErr})
}

// ---- admin: global settings -------------------------------------------------

func HandleAdminSettings(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s, err := store.GetSettings(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		ok(w, map[string]any{"settings": s})

	case http.MethodPut:
		var body struct {
			SessionDurationHours int  `json:"session_duration_hours"`
			RenewOnAccess        bool `json:"renew_on_access"`
		}
		if !decode(r, &body) {
			fail(w, http.StatusBadRequest, "invalid request")
			return
		}
		if err := store.UpdateSettings(r.Context(), body.SessionDurationHours, body.RenewOnAccess); err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if OnRouteUpdate != nil {
			OnRouteUpdate() // refreshes auth cache including globalSettings
		}
		ok(w, map[string]bool{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
