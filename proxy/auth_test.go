package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reMazarin/storage"
	"strconv"
	"testing"
	"time"
)

// A route's persistent_login flag gates whether cookie (persistent-login) auth is
// honoured at all. With it off, a valid session cookie must be ignored and the
// request denied — even though the cookie itself is untouched — so the route is
// reachable only via IP session auth. With it on, the same cookie authenticates.
func TestPersistentLoginGatesCookieAuth(t *testing.T) {
	ctx := context.Background()
	s, err := storage.New(t.TempDir() + "/gate.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	u, _ := s.CreateUser(ctx, "meng", "pw")
	g, _ := s.CreateGroup(ctx, "g1", "")
	s.AddUserToGroup(ctx, u.ID, g.ID)
	// Session created from a different IP than the request will use, so IP auth
	// cannot grant access — only the cookie can, exercising the gate in isolation.
	tok, err := s.CreateSession(ctx, u.ID, time.Hour, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	authStore = s
	globalSettings.Store(storage.Settings{SessionDurationHours: 168, RenewOnAccess: true})

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	do := func(persistent bool) int {
		authCache.Store(map[string]cachedRoute{
			"x": parseCachedRoute(storage.Route{
				Url:             "x",
				AllowedGroups:   strconv.Itoa(g.ID),
				PersistentLogin: persistent,
			}),
		})
		req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		req.RemoteAddr = "9.9.9.9:1234" // not the session IP
		rec := httptest.NewRecorder()
		withAuthForKey("x", next).ServeHTTP(rec, req)
		return rec.Code
	}

	if code := do(false); code != http.StatusProxyAuthRequired {
		t.Fatalf("persistent_login off: want 407 (cookie ignored), got %d", code)
	}
	if code := do(true); code != http.StatusOK {
		t.Fatalf("persistent_login on: want 200 (cookie honoured), got %d", code)
	}
}
