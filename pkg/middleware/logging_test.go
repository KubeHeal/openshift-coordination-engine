package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestRequestLogger(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Suppress logs during tests

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := RequestLogger(log)
	wrappedHandler := middleware(handler)

	req := httptest.NewRequest("GET", "/test", http.NoBody)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.NotEmpty(t, rr.Header().Get(RequestIDHeader))
}

func TestRequestLogger_WithRequestID(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLogger(log)
	wrappedHandler := middleware(handler)

	req := httptest.NewRequest("GET", "/test", http.NoBody)
	req.Header.Set(RequestIDHeader, "test-request-id")
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	assert.Equal(t, "test-request-id", rr.Header().Get(RequestIDHeader))
}

func TestRequestLogger_StatusCode(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		expectedStatus int
	}{
		{"OK", http.StatusOK, http.StatusOK},
		{"Created", http.StatusCreated, http.StatusCreated},
		{"BadRequest", http.StatusBadRequest, http.StatusBadRequest},
		{"InternalError", http.StatusInternalServerError, http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logrus.New()
			log.SetLevel(logrus.ErrorLevel)

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			})

			middleware := RequestLogger(log)
			wrappedHandler := middleware(handler)

			req := httptest.NewRequest("GET", "/test", http.NoBody)
			rr := httptest.NewRecorder()

			wrappedHandler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)
		})
	}
}
