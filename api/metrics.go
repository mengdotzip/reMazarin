package api

import (
	"net/http"
	"reMazarin/storage"
	"strconv"
)

// RouteStats is wired from main.go to proxy.GetRouteStats.
var RouteStats func() map[string]int64

func HandleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		sessions, err := store.GetActiveSessions(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if sessions == nil {
			sessions = []storage.SessionInfo{}
		}
		failures, err := store.GetRecentAuthFailures(r.Context(), 100)
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if failures == nil {
			failures = []storage.AuthFailure{}
		}
		var stats map[string]int64
		if RouteStats != nil {
			stats = RouteStats()
		}
		if stats == nil {
			stats = map[string]int64{}
		}
		accessLog, err := store.GetRecentAccess(r.Context(), 2000)
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if accessLog == nil {
			accessLog = []storage.AccessEvent{}
		}
		ok(w, map[string]any{
			"sessions":      sessions,
			"route_stats":   stats,
			"auth_failures": failures,
			"access_log":    accessLog,
		})

	case http.MethodDelete:
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := store.AdminDeleteSession(r.Context(), id); err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		ok(w, map[string]bool{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func HandleUserSessions(w http.ResponseWriter, r *http.Request) {
	sess, err := sessionFromRequest(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sessions, err := store.GetUserSessions(r.Context(), sess.UserID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if sessions == nil {
			sessions = []storage.SessionInfo{}
		}
		ok(w, map[string]any{"sessions": sessions, "current_id": sess.ID})

	case http.MethodDelete:
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := store.DeleteSessionByID(r.Context(), id, sess.UserID); err != nil {
			fail(w, http.StatusNotFound, "session not found")
			return
		}
		ok(w, map[string]bool{"ok": true})

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
