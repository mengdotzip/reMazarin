package api

import (
	"log/slog"
	"net/http"

	"github.com/mdobak/go-xerrors"
)

type APIHandler func(http.ResponseWriter, *http.Request)

var registry = make(map[string]APIHandler)

func InitApi() {
	slog.Debug("initializing api handlers")

	//////////////////////////////////////////////////////////////////////////////////////////
	//   _______  ________________  __  ___  ______  ___  _____________________  _  ______  /
	//  / ___/ / / / __/_  __/ __ \/  |/  / / __/ / / / |/ / ___/_  __/  _/ __ \/ |/ / __/ /
	// / /__/ /_/ /\ \  / / / /_/ / /|_/ / / _// /_/ /    / /__  / / _/ // /_/ /    /\ \  /
	// \___/\____/___/ /_/  \____/_/  /_/ /_/  \____/_/|_/\___/ /_/ /___/\____/_/|_/___/ /
	/////////////////////////////////////////////////////////////////////////////////////
	//ADD YOUR CUSTOM FUNCTION HERE//
	////////////////////////////////
	register("example", HandleExample)
	register("health", HandleHealth)
	/////////////////////////////
	////////////////////////////
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
