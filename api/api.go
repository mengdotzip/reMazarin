package api

import (
	"log/slog"
	"net/http"

	"github.com/mdobak/go-xerrors"
)

type APIHandler func(http.ResponseWriter, *http.Request)

var registry = make(map[string]APIHandler)

// InitBuiltin registers the built-in auth and admin API handlers.
// Do not modify this — add your own handlers in InitApi below.
func InitBuiltin() error {
	for _, e := range []struct {
		name string
		h    APIHandler
	}{
		{"config", HandleConfig},
		{"auth/login", HandleLogin},
		{"auth/logout", HandleLogout},
		{"auth/register", HandleRegister},
		{"auth/me", HandleMe},
		{"auth/routes", HandleUserRoutes},
		{"admin/users", HandleAdminUsers},
		{"admin/users/groups", HandleAdminUserGroups},
		{"admin/groups", HandleAdminGroups},
		{"admin/invites", HandleAdminInvites},
		{"admin/routes", HandleAdminRoutes},
		{"admin/metrics", HandleAdminMetrics},
		{"auth/sessions", HandleUserSessions},
	} {
		if err := register(e.name, e.h); err != nil {
			return err
		}
	}
	return nil
}

// InitApi registers user-defined API handlers.
// Handlers added here are reachable as /api/<name> on static routes
// and as the target of type="api" routes in config.toml.
func InitApi() error {
	slog.Debug("initializing api handlers")

	//////////////////////////////////////////////////////////////////////////////////////////
	//   _______  ________________  __  ___  ______  ___  _____________________  _  ______  /
	//  / ___/ / / / __/_  __/ __ \/  |/  / / __/ / / / |/ / ___/_  __/  _/ __ \/ |/ / __/ /
	// / /__/ /_/ /\ \  / / / /_/ / /|_/ / / _// /_/ /    / /__  / / _/ // /_/ /    /\ \  /
	// \___/\____/___/ /_/  \____/_/  /_/ /_/  \____/_/|_/\___/ /_/ /___/\____/_/|_/___/ /
	/////////////////////////////////////////////////////////////////////////////////////
	// ADD YOUR CUSTOM FUNCTIONS HERE                                                    /
	/////////////////////////////////////////////////////////////////////////////////////
	if err := register("example", HandleExample); err != nil {
		return err
	}
	if err := register("health", HandleHealth); err != nil {
		return err
	}
	return nil
	/////////////////////////////////////////////////////////////////////////////////////
}

func register(name string, handler APIHandler) error {
	if _, exists := registry[name]; exists {
		return xerrors.Newf("api handler already registered: %s", name)
	}

	registry[name] = handler
	slog.Debug("api handler registered", "name", name)
	return nil
}

func Get(name string) (APIHandler, error) {

	handler, exists := registry[name]
	if !exists {
		return nil, xerrors.Newf("api handler not found: %s", name)
	}

	return handler, nil
}
