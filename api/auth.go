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

func setSession(w http.ResponseWriter, r *http.Request, tok string, dur time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		Domain:   rootDomain(r.Host),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(dur.Seconds()),
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
	dur := defaultSessionDur
	if webRoute, err := store.GetRouteByUrl(r.Context(), authURL); err == nil && webRoute.SessionDuration > 0 {
		dur = time.Duration(webRoute.SessionDuration) * time.Hour
	}
	tok, err := store.CreateSession(r.Context(), user.ID, dur, clientIP)
	if err != nil {
		fail(w, http.StatusInternalServerError, "session error")
		return
	}
	setSession(w, r, tok, dur)
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
	// Exclude the proxy/auth page itself by comparing hostnames.
	selfHost := strings.SplitN(r.Host, ":", 2)[0]
	var accessible []routeInfo
	for _, rt := range routes {
		// Skip tcp routes and internal API handlers — not browser-navigable.
		if rt.Type == "tcp" || rt.Type == "api" {
			continue
		}
		// Skip this proxy/auth page.
		if strings.SplitN(rt.Url, ":", 2)[0] == selfHost {
			continue
		}
		if storage.RouteAllows(rt.AllowedGroups, groupIDs) {
			accessible = append(accessible, routeInfo{rt.Url, rt.Tls, rt.SessionDuration, rt.RenewOnAccess})
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
	dur := time.Duration(rt.SessionDuration) * time.Hour
	if dur <= 0 {
		dur = defaultSessionDur
	}
	store.ExtendSessionByID(r.Context(), sess.ID, dur)
	// Refresh the browser cookie so it doesn't expire before the DB session.
	if c, err := r.Cookie(sessionCookie); err == nil {
		setSession(w, r, c.Value, dur)
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
			URL    string `json:"url"`
			Target string `json:"target"`
			Type   string `json:"type"`
			Tls    bool   `json:"tls"`
		}
		if !decode(r, &body) || body.URL == "" || body.Target == "" {
			fail(w, http.StatusBadRequest, "url and target required")
			return
		}
		if body.Type == "" {
			body.Type = "proxy"
		}
		cert, key := "", ""
		if body.Tls {
			cert, key = DefaultCert, DefaultKey
		}
		route, err := store.CreateRoute(r.Context(), body.URL, body.Target, body.Type, body.Tls, cert, key)
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
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body struct {
			AllowedGroups   string `json:"allowed_groups"`
			AllowedIPs      string `json:"allowed_ips"`
			IPAuth          bool   `json:"ip_auth"`
			CookiePolicy    string `json:"cookie_policy"`
			RenewOnAccess   bool   `json:"renew_on_access"`
			SessionDuration int    `json:"session_duration"`
			Target          string `json:"target"`
		}
		if !decode(r, &body) {
			fail(w, http.StatusBadRequest, "invalid request")
			return
		}
		if body.CookiePolicy == "" {
			body.CookiePolicy = "persistent"
		}
		if err := store.UpdateRouteAccess(r.Context(), id, body.AllowedGroups, body.AllowedIPs, body.IPAuth, body.CookiePolicy, body.RenewOnAccess, body.SessionDuration); err != nil {
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
