package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/KubeHeal/openshift-coordination-engine/internal/detector"
	"github.com/KubeHeal/openshift-coordination-engine/internal/remediation"
	"github.com/KubeHeal/openshift-coordination-engine/internal/storage"
	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

type apiStubRemediator struct{}

func (apiStubRemediator) Remediate(context.Context, *models.DeploymentInfo, *models.Issue) error {
	return nil
}

func (apiStubRemediator) CanRemediate(*models.DeploymentInfo) bool { return true }

func (apiStubRemediator) Name() string { return "stub" }

func TestTriggerWritesWorkflowIDAndListFilters(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	det := detector.NewDetector(fake.NewSimpleClientset(), log)
	orchestrator := remediation.NewOrchestrator(det, apiStubRemediator{}, log, t.TempDir())
	store := storage.NewIncidentStore()
	handler := NewRemediationHandlerWithStore(orchestrator, store, log)

	created, err := store.Create(&models.Incident{
		Title:       "checkout down",
		Description: "pods are crash looping",
		Severity:    models.IncidentSeverityCritical,
		Target:      "checkout",
	})
	require.NoError(t, err)

	body := []byte(`{
		"incident_id": "` + created.ID + `",
		"namespace": "checkout",
		"resource": {"kind": "Deployment", "name": "checkout-api"},
		"issue": {"type": "pod_crash_loop", "severity": "critical", "description": "crash"}
	}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/remediation/trigger", bytes.NewReader(body))
	handler.TriggerRemediation(recorder, request)
	require.Equal(t, http.StatusAccepted, recorder.Code)

	var triggered TriggerRemediationResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &triggered))
	require.Equal(t, "1m", triggered.EstimatedDuration)
	require.NotEmpty(t, triggered.WorkflowID)

	stored, err := store.Get(created.ID)
	require.NoError(t, err)
	require.Equal(t, triggered.WorkflowID, stored.WorkflowID)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?severity=all&status=all&limit=10", http.NoBody)
	listRec := httptest.NewRecorder()
	handler.ListIncidents(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listed ListIncidentsResponse
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &listed))
	require.GreaterOrEqual(t, listed.Total, 1)
	var workflowRow *IncidentListItem
	for i := range listed.Incidents {
		if listed.Incidents[i].Source == "workflow" {
			workflowRow = &listed.Incidents[i]
		}
	}
	require.NotNil(t, workflowRow)
	require.Equal(t, "critical", workflowRow.Severity)

	require.Eventually(t, func() bool {
		getReq := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+triggered.WorkflowID, http.NoBody), map[string]string{"id": triggered.WorkflowID})
		getRec := httptest.NewRecorder()
		handler.GetWorkflow(getRec, getReq)
		if getRec.Code != http.StatusOK {
			return false
		}
		var workflow WorkflowResponse
		if err := json.Unmarshal(getRec.Body.Bytes(), &workflow); err != nil {
			return false
		}
		return len(workflow.Layers) == 1 && workflow.Layers[0] == "application" &&
			len(workflow.Checkpoints) == 1 && workflow.Checkpoints[0].Status == models.CheckpointPassed
	}, 2*time.Second, 20*time.Millisecond)
}
