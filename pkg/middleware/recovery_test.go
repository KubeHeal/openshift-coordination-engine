package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestRecovery_NoPanic(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := Recovery(log)
	wrappedHandler := middleware(handler)

	req := httptest.NewRequest("GET", "/test", http.NoBody)
	rr := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestRecovery_WithPanic(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	middleware := Recovery(log)
	wrappedHandler := middleware(handler)

	req := httptest.NewRequest("GET", "/test", http.NoBody)
	rr := httptest.NewRecorder()

	// Should not panic, should return 500
	wrappedHandler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Contains(t, rr.Body.String(), "Internal server error")
}
