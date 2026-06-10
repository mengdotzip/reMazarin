package api

import (
	"net/http"
	"reMazarin/storage"
	"strconv"
)

// These are wired from main.go to the corresponding proxy functions. They are
// function vars (not direct imports) because proxy imports api — a direct
// dependency the other way would be an import cycle.
var (
	RouteStats   func() map[string]int64   // proxy.GetRouteStats
	EventStats   func() map[string]int64   // proxy.GetEventStats
	RecentEvents func() any                // proxy.GetRecentEvents
	ActiveBans   func() []storage.BannedIP // proxy.GetActiveBans
)

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
		var eventStats map[string]int64
		if EventStats != nil {
			eventStats = EventStats()
		}
		if eventStats == nil {
			eventStats = map[string]int64{}
		}
		var recentEvents any = []any{}
		if RecentEvents != nil {
			if re := RecentEvents(); re != nil {
				recentEvents = re
			}
		}
		var bans []storage.BannedIP
		if ActiveBans != nil {
			bans = ActiveBans()
		}
		if bans == nil {
			bans = []storage.BannedIP{}
		}
		ok(w, map[string]any{
			"sessions":      sessions,
			"route_stats":   stats,
			"auth_failures": failures,
			"access_log":    accessLog,
			"event_stats":   eventStats,
			"recent_events": recentEvents,
			"banned_ips":    bans,
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
