package proxy

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mdobak/go-xerrors"
)

func createStaticHandler(route *ProxyRoute) (http.Handler, error) {
	fi, err := os.Stat(route.Target)
	if err != nil {
		return nil, xerrors.Newf("cant open static path %s: %w", route.Target, err)
	}

	switch mode := fi.Mode(); {
	case mode.IsDir():
		return createFolderHandler(route)
	case mode.IsRegular():
		return createFileHandler(route)
	default:
		return nil, xerrors.Newf("unsupported file type: %s", route.Target)
	}
}

func createFileHandler(route *ProxyRoute) (http.Handler, error) {
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

func createFolderHandler(route *ProxyRoute) (http.Handler, error) {
	root, err := os.OpenRoot(route.Target)
	if err != nil {
		return nil, xerrors.Newf("open root %s: %w", route.Target, err)
	}

	fsys := root.FS()
	slog.Info("static folder handler created",
		"folder", route.Target,
	)

	return http.FileServerFS(fsys), nil
}
