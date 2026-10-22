package v1

// APIErrorResponse is a JSON error body used by several endpoints.
type APIErrorResponse struct {
	Status string `json:"status" example:"error"`
	Error  string `json:"error"`
}
