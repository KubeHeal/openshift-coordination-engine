package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/sirupsen/logrus"
)

// Recovery creates a middleware that recovers from panics
func Recovery(log *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					// Log the panic with stack trace
					log.WithFields(logrus.Fields{
						"error":      fmt.Sprintf("%v", err),
						"stack":      string(debug.Stack()),
						"method":     r.Method,
						"path":       r.URL.Path,
						"request_id": r.Header.Get(RequestIDHeader),
					}).Error("Panic recovered in HTTP handler")

					// Return 500 Internal Server Error
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					if _, err := w.Write([]byte(`{"error":"Internal server error","message":"An unexpected error occurred"}`)); err != nil {
						log.WithError(err).Error("Failed to write panic recovery response")
					}
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
