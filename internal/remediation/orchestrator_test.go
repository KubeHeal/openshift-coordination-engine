package remediation

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/KubeHeal/openshift-coordination-engine/internal/detector"
	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

type stubRemediator struct {
	err error
}

func (s stubRemediator) Remediate(context.Context, *models.DeploymentInfo, *models.Issue) error {
	return s.err
}

func (s stubRemediator) CanRemediate(*models.DeploymentInfo) bool { return true }

func (s stubRemediator) Name() string { return "stub" }

func TestOrchestratorPersistsWorkflowAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	log := logrus.New()
	log.SetOutput(io.Discard)
	det := detector.NewDetector(fake.NewSimpleClientset(), log)

	orchestrator := NewOrchestrator(det, stubRemediator{}, log, dir)
	workflow, err := orchestrator.TriggerRemediation(context.Background(), "inc-1", &models.Issue{
		ID:           "inc-1",
		Type:         "pod_crash_loop",
		Severity:     "critical",
		Namespace:    "checkout",
		ResourceType: "Deployment",
		ResourceName: "checkout-api",
	})
	require.NoError(t, err)
	require.Equal(t, "1m", workflow.EstimatedDuration)
	require.Equal(t, []string{"application"}, workflow.Layers)
	require.Equal(t, "critical", workflow.Severity)

	require.Eventually(t, func() bool {
		current, getErr := orchestrator.GetWorkflow(workflow.ID)
		return getErr == nil && len(current.Checkpoints) == 1 && current.Checkpoints[0].Status == models.CheckpointPassed
	}, 2*time.Second, 20*time.Millisecond)

	reloaded := NewOrchestrator(det, stubRemediator{}, log, dir)
	got, err := reloaded.GetWorkflow(workflow.ID)
	require.NoError(t, err)
	require.Equal(t, models.CheckpointPassed, got.Checkpoints[0].Status)
	require.Equal(t, "1m", got.EstimatedDuration)
	require.Equal(t, "checkout", got.Namespace)
}
