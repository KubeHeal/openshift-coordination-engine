package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/KubeHeal/openshift-coordination-engine/internal/policy"
)

const maxAlertWebhookBytes = 2 << 20

// AlertsHandler accepts Alertmanager webhooks and runs the policy engine.
type AlertsHandler struct {
	engine *policy.Engine
	log    *logrus.Logger
}

// NewAlertsHandler returns a handler for POST /api/v1/alerts.
func NewAlertsHandler(engine *policy.Engine, log *logrus.Logger) *AlertsHandler {
	if log == nil {
		log = logrus.New()
	}
	return &AlertsHandler{engine: engine, log: log}
}

// RegisterRoutes registers the policy-engine alert webhook.
func (h *AlertsHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/api/v1/alerts", h.PostAlerts).Methods(http.MethodPost)
}

// PostAlerts handles an Alertmanager v4 webhook.
func (h *AlertsHandler) PostAlerts(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		h.writeError(w, http.StatusBadRequest, "Invalid Alertmanager webhook format")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAlertWebhookBytes)
	var payload alertmanagerWebhook
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.log.WithError(err).Debug("invalid alertmanager webhook")
		h.writeError(w, http.StatusBadRequest, "Invalid Alertmanager webhook format")
		return
	}
	response := h.engine.HandleWebhook(r.Context(), payload.toPolicyWebhook())
	h.writeJSON(w, http.StatusOK, response)
}

func (h *AlertsHandler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{
		"status": "error",
		"error":  message,
	})
}

func (h *AlertsHandler) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.log.WithError(err).Error("encode alerts response")
	}
}

type alertmanagerWebhook struct {
	Status string              `json:"status"`
	Alerts []alertmanagerAlert `json:"alerts"`
}

type alertmanagerAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	StartsAt    time.Time         `json:"startsAt"`
	Fingerprint string            `json:"fingerprint"`
}

func (w alertmanagerWebhook) toPolicyWebhook() policy.Webhook {
	alerts := make([]policy.IncomingAlert, 0, len(w.Alerts))
	for _, alert := range w.Alerts {
		alerts = append(alerts, policy.IncomingAlert{
			Status:      alert.Status,
			Labels:      alert.Labels,
			StartsAt:    alert.StartsAt,
			Fingerprint: alert.Fingerprint,
		})
	}
	return policy.Webhook{Status: w.Status, Alerts: alerts}
}
