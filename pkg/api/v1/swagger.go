package v1

import (
	"github.com/gorilla/mux"
	httpSwagger "github.com/swaggo/http-swagger"

	_ "github.com/KubeHeal/openshift-coordination-engine/docs" // registers the generated spec
)

// RegisterSwaggerUI serves Swagger UI at /api/docs/ from the generated OpenAPI spec.
func RegisterSwaggerUI(router *mux.Router) {
	router.PathPrefix("/api/docs/").Handler(httpSwagger.Handler(httpSwagger.URL("/api/docs/doc.json")))
}
