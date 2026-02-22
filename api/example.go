package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// HandleExample is a simple example API handler
func HandleExample(w http.ResponseWriter, r *http.Request) {
	slog.Info("example api called",
		"method", r.Method,
		"path", r.URL.Path,
	)

	response := map[string]interface{}{
		"message": "Hello from API handler!",
		"path":    r.URL.Path,
		"method":  r.Method,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleHealth is a health check endpoint
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"handler": "api",
	})
}
