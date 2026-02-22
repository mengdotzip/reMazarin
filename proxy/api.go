package proxy

import (
	"log/slog"
	"net/http"
	"reMazarin/api"

	"github.com/mdobak/go-xerrors"
)

func createAPIHandler(route *ProxyRoute) (http.Handler, error) {
	handler, err := api.Get(route.Target)
	if err != nil {
		return nil, xerrors.Newf("api function %s: %w", route.Target, err)
	}

	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("api handler called",
			"function", route.Target,
			"path", r.URL.Path,
		)
		handler(w, r)
	})

	slog.Info("api handler created", "function", route.Target)
	return wrapped, nil
}
