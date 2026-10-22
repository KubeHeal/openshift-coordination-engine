package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

func TestRegisterSwaggerUI(t *testing.T) {
	router := mux.NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/docs/index.html", nil)

	off := httptest.NewRecorder()
	router.ServeHTTP(off, req)
	require.Equal(t, http.StatusNotFound, off.Code)

	RegisterSwaggerUI(router)
	on := httptest.NewRecorder()
	router.ServeHTTP(on, req)
	require.Equal(t, http.StatusOK, on.Code)
	require.NotEmpty(t, on.Body.String())
}
