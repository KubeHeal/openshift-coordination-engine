package storage

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

func TestWorkflowStoreRoundTripSkipsIncidentLoad(t *testing.T) {
	dir := t.TempDir()
	log := logrus.New()
	log.SetOutput(io.Discard)

	store, err := NewWorkflowStore(dir, log)
	require.NoError(t, err)

	workflow := &models.Workflow{
		ID:                "wf-abc12345",
		IncidentID:        "inc-1",
		Status:            models.WorkflowStatusPending,
		Severity:          "high",
		EstimatedDuration: "1m",
		Layers:            []string{"application"},
		Checkpoints: []models.WorkflowCheckpoint{{
			Layer:     "application",
			AfterStep: 1,
			Status:    models.CheckpointPending,
			Checks:    []string{"pods_running", "endpoints_healthy"},
		}},
	}
	require.NoError(t, store.Save(workflow))

	loaded, err := store.Load()
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	require.Equal(t, "1m", loaded[0].EstimatedDuration)
	require.Equal(t, []string{"application"}, loaded[0].Layers)
	require.Equal(t, models.CheckpointPending, loaded[0].Checkpoints[0].Status)

	incidents, err := NewIncidentStoreWithPersistence(dir, 0, log)
	require.NoError(t, err)
	require.Equal(t, 0, incidents.Count())
	require.FileExists(t, filepath.Join(dir, "workflows", "wf-abc12345.json"))
}
