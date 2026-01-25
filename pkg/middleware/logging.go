package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

const (
	// RequestIDHeader is the header name for request ID
	RequestIDHeader = "X-Request-ID"
)

// RequestIDKey is the context key for request ID
type contextKey string

// RequestIDKey is the context key for storing request IDs
const RequestIDKey contextKey = "request_id"

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	if err != nil {
		return n, fmt.Errorf("failed to write response: %w", err)
	}
	return n, nil
}

// RequestLogger creates a middleware that logs HTTP requests
func RequestLogger(log *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Generate or extract request ID
			requestID := r.Header.Get(RequestIDHeader)
			if requestID == "" {
				requestID = uuid.New().String()
			}

			// Add request ID to response headers
			w.Header().Set(RequestIDHeader, requestID)

			// Wrap response writer to capture status code
			rw := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// Log request start
			log.WithFields(logrus.Fields{
				"request_id":  requestID,
				"method":      r.Method,
				"path":        r.URL.Path,
				"remote_addr": r.RemoteAddr,
				"user_agent":  r.UserAgent(),
			}).Debug("Request started")

			// Process request
			next.ServeHTTP(rw, r)

			// Calculate duration
			duration := time.Since(start)

			// Log request completion
			logEntry := log.WithFields(logrus.Fields{
				"request_id":    requestID,
				"method":        r.Method,
				"path":          r.URL.Path,
				"status":        rw.statusCode,
				"duration_ms":   duration.Milliseconds(),
				"bytes_written": rw.written,
				"remote_addr":   r.RemoteAddr,
			})

			// Log level based on status code
			switch {
			case rw.statusCode >= 500:
				logEntry.Error("Request completed with server error")
			case rw.statusCode >= 400:
				logEntry.Warn("Request completed with client error")
			default:
				logEntry.Info("Request completed")
			}
		})
	}
}
