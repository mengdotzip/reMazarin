package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mdobak/go-xerrors"
)

func setupLogging() *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Value.Kind() == slog.KindAny {
				if err, ok := a.Value.Any().(error); ok {
					a.Value = slog.GroupValue(
						slog.String("msg", err.Error()),
						slog.Any("trace", getFrames(err)),
					)
				}
			}
			return a
		},
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func getFrames(err error) []map[string]any {
	frames := xerrors.StackTrace(err).Frames()
	if len(frames) == 0 {
		return nil
	}

	result := make([]map[string]any, len(frames))
	for i, f := range frames {
		result[i] = map[string]any{
			"func":   filepath.Base(f.Function),
			"source": filepath.Base(f.File),
			"line":   f.Line,
		}
	}
	return result
}
