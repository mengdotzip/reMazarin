package api

import (
	"net/http"
	"reMazarin/storage"
	"strings"
)

// BanIP / UnbanIP are wired from main.go to the proxy so the in-memory ban set
// and the DB stay in sync (function vars avoid the proxy→api import cycle).
var (
	BanIP   func(ip string, durationSec int)
	UnbanIP func(ip string)
)

// HandleAdminThrottle manages per-tier throttle/auto-ban policies and the IP ban
// list.
//
//	GET                      → { policies, bans }
//	PUT                      → upsert a tier policy (body = ThrottlePolicy)
//	POST                     → manually ban an IP { ip, duration_sec, reason }
//	DELETE ?ip=<ip>          → unban an IP
//	DELETE ?tier=group:<id>  → delete a per-group policy override
func HandleAdminThrottle(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		policies, err := store.GetThrottlePolicies(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if policies == nil {
			policies = []storage.ThrottlePolicy{}
		}
		bans, err := store.GetActiveBans(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if bans == nil {
			bans = []storage.BannedIP{}
		}
		ok(w, map[string]any{"policies": policies, "bans": bans})

	case http.MethodPut:
		var p storage.ThrottlePolicy
		if !decode(r, &p) || strings.TrimSpace(p.Tier) == "" {
			fail(w, http.StatusBadRequest, "tier required")
			return
		}
		// Only the two built-in tiers and 'group:<id>' overrides are valid.
		if p.Tier != storage.TierAnonymous && p.Tier != storage.TierSignedIn &&
			!strings.HasPrefix(p.Tier, "group:") {
			fail(w, http.StatusBadRequest, "invalid tier")
			return
		}
		if err := store.UpsertThrottlePolicy(r.Context(), p); err != nil {
			fail(w, http.StatusInternalServerError, "db error")
			return
		}
		if OnRouteUpdate != nil {
			OnRouteUpdate() // reloads the proxy throttle snapshot
		}
		ok(w, map[string]bool{"ok": true})

	case http.MethodPost:
		var body struct {
			IP          string `json:"ip"`
			DurationSec int    `json:"duration_sec"`
		}
		if !decode(r, &body) || strings.TrimSpace(body.IP) == "" {
			fail(w, http.StatusBadRequest, "ip required")
			return
		}
		if BanIP == nil {
			fail(w, http.StatusInternalServerError, "ban unavailable")
			return
		}
		BanIP(strings.TrimSpace(body.IP), body.DurationSec)
		ok(w, map[string]bool{"ok": true})

	case http.MethodDelete:
		if ip := r.URL.Query().Get("ip"); ip != "" {
			if UnbanIP != nil {
				UnbanIP(ip)
			}
			ok(w, map[string]bool{"ok": true})
			return
		}
		if tier := r.URL.Query().Get("tier"); tier != "" {
			if err := store.DeleteThrottlePolicy(r.Context(), tier); err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
			if OnRouteUpdate != nil {
				OnRouteUpdate()
			}
			ok(w, map[string]bool{"ok": true})
			return
		}
		fail(w, http.StatusBadRequest, "ip or tier required")

	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
