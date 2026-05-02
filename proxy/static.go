package proxy

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reMazarin/api"
	"strings"

	"github.com/mdobak/go-xerrors"
)

func createStaticHandler(route *ProxyRoute) (http.Handler, error) {
	fi, err := os.Stat(route.Target)
	if err != nil {
		return nil, xerrors.Newf("cant open static path %s: %w", route.Target, err)
	}

	var inner http.Handler
	switch {
	case fi.Mode().IsDir():
		inner, err = folderHandler(route)
	case fi.Mode().IsRegular():
		inner, err = fileHandler(route)
	default:
		return nil, xerrors.Newf("unsupported file type: %s", route.Target)
	}
	if err != nil {
		return nil, err
	}

	return &staticWithAPI{static: inner}, nil
}

// staticWithAPI serves /api/* requests from the API registry and everything
// else from the static file handler.
type staticWithAPI struct {
	static http.Handler
}

func (h *staticWithAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		name := strings.TrimPrefix(r.URL.Path, "/api/")
		handler, err := api.Get(name)
		if err != nil {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		handler(w, r)
		return
	}
	h.static.ServeHTTP(w, r)
}

func fileHandler(route *ProxyRoute) (http.Handler, error) {
	dir := filepath.Dir(route.Target)
	filename := filepath.Base(route.Target)

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, xerrors.Newf("open root %s: %w", dir, err)
	}
	fsys := root.FS()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("serving static file", "file", filename, "path", r.URL.Path)
		http.ServeFileFS(w, r, fsys, filename)
	})
	slog.Info("static file handler created", "file", route.Target)
	return handler, nil
}

func folderHandler(route *ProxyRoute) (http.Handler, error) {
	root, err := os.OpenRoot(route.Target)
	if err != nil {
		return nil, xerrors.Newf("open root %s: %w", route.Target, err)
	}
	slog.Info("static folder handler created", "folder", route.Target)
	return http.FileServerFS(root.FS()), nil
}
