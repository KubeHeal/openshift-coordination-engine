package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestDryRunDoesNotExecute(t *testing.T) {
	handler, store, orchestrator := newContractHandler(t, apiStubRemediator{})
	created := mustCreateIncident(t, store, "checkout")

	recorder := postTrigger(handler, `{
		"incident_id": "`+created.ID+`",
		"namespace": "checkout",
		"resource": {"kind": "Deployment", "name": "checkout-api"},
		"issue": {"type": "pod_crash_loop", "severity": "high", "description": "crash"},
		"dry_run": true
	}`)
	require.Equal(t, http.StatusAccepted, recorder.Code)

	var triggered TriggerRemediationResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &triggered))
	require.Empty(t, triggered.WorkflowID)
	require.Equal(t, "pending", triggered.Status)
	require.Equal(t, "1m", triggered.EstimatedDuration)
	require.Equal(t, "unknown", triggered.DeploymentMethod)
	require.Empty(t, orchestrator.ListWorkflows())

	stored, err := store.Get(created.ID)
	require.NoError(t, err)
	require.Empty(t, stored.WorkflowID)
}

func TestIncidentListMatchesMCPContract(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	handler, store, _ := newContractHandler(t, holdRemediator{started: started, release: release})

	active := mustCreateIncident(t, store, "checkout")
	for i := 0; i < 2; i++ {
		mustCreateIncident(t, store, "other")
	}

	recorder := postTrigger(handler, `{
		"incident_id": "`+active.ID+`",
		"namespace": "checkout",
		"resource": {"kind": "Deployment", "name": "checkout-api"},
		"issue": {"type": "pod_crash_loop", "severity": "critical", "description": "crash"}
	}`)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("remediation did not start")
	}

	activeList := listIncidents(t, handler, "/api/v1/incidents?status=active&severity=all")
	require.GreaterOrEqual(t, activeList.Summary.Active, 2)
	require.Equal(t, activeList.Summary.Total, activeList.Total)
	var sawStored, sawRunning bool
	for i := range activeList.Incidents {
		row := &activeList.Incidents[i]
		if row.Source == "manual" && row.ID == active.ID && row.Status == "active" {
			sawStored = true
		}
		if row.Source == "workflow" && row.Status == "running" && row.ActionType == "pod_crash_loop" {
			sawRunning = true
		}
	}
	require.True(t, sawStored)
	require.True(t, sawRunning)

	page := listIncidents(t, handler, "/api/v1/incidents?status=all&severity=all&limit=1&offset=1")
	require.Len(t, page.Incidents, 1)
	require.GreaterOrEqual(t, page.Summary.Total, 3)
	require.Equal(t, page.Summary.Total, page.Total)

	finish()

	require.Eventually(t, func() bool {
		completed := listIncidents(t, handler, "/api/v1/incidents?status=completed&severity=all")
		for i := range completed.Incidents {
			row := &completed.Incidents[i]
			if row.Source == "workflow" && row.Status == "completed" && row.ActionType == "pod_crash_loop" && row.DurationSeconds > 0 {
				return completed.Summary.Completed >= 1
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond)
}

type holdRemediator struct {
	started chan struct{}
	release chan struct{}
}

func (h holdRemediator) Remediate(context.Context, *models.DeploymentInfo, *models.Issue) error {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.release
	return nil
}

func (holdRemediator) CanRemediate(*models.DeploymentInfo) bool { return true }

func (holdRemediator) Name() string { return "hold" }

func newContractHandler(t *testing.T, remediator remediation.Remediator) (*RemediationHandler, *storage.IncidentStore, *remediation.Orchestrator) {
	t.Helper()
	log := logrus.New()
	log.SetOutput(io.Discard)
	det := detector.NewDetector(fake.NewSimpleClientset(), log)
	orchestrator := remediation.NewOrchestrator(det, remediator, log, t.TempDir())
	store := storage.NewIncidentStore()
	return NewRemediationHandlerWithStore(orchestrator, store, log), store, orchestrator
}

func mustCreateIncident(t *testing.T, store *storage.IncidentStore, target string) *models.Incident {
	t.Helper()
	created, err := store.Create(&models.Incident{
		Title:       "checkout down",
		Description: "pods are crash looping",
		Severity:    models.IncidentSeverityCritical,
		Target:      target,
	})
	require.NoError(t, err)
	return created
}

func postTrigger(handler *RemediationHandler, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/remediation/trigger", bytes.NewReader([]byte(body)))
	handler.TriggerRemediation(recorder, request)
	return recorder
}

func listIncidents(t *testing.T, handler *RemediationHandler, rawURL string) ListIncidentsResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ListIncidents(recorder, httptest.NewRequest(http.MethodGet, rawURL, http.NoBody))
	require.Equal(t, http.StatusOK, recorder.Code)
	var listed ListIncidentsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &listed))
	return listed
}
