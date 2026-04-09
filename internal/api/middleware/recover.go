package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover catches panics and returns 500 JSON without crashing the process.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("handler panic", "recover", rec, "stack", string(debug.Stack()))
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "internal server error",
					},
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
