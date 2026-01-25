package coordination

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestNewHealthChecker(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	log := logrus.New()

	// Pass nil for dynamic client in tests (gracefully handled by health checker)
	hc := NewHealthChecker(clientset, nil, log)

	assert.NotNil(t, hc)
	assert.NotNil(t, hc.clientset)
	assert.NotNil(t, hc.log)
}

func TestHealthChecker_CheckInfrastructureHealth(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Suppress debug logs in tests

	// Pass nil for dynamic client - it will skip OpenShift-specific checks
	hc := NewHealthChecker(clientset, nil, log)
	ctx := context.Background()

	// Should pass with empty cluster (no nodes is acceptable)
	err := hc.CheckInfrastructureHealth(ctx)
	assert.NoError(t, err)
}

func TestHealthChecker_CheckPlatformHealth(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Pass nil for dynamic client - it will skip OpenShift-specific checks
	hc := NewHealthChecker(clientset, nil, log)
	ctx := context.Background()

	// Should pass with OpenShift checks skipped
	err := hc.CheckPlatformHealth(ctx)
	assert.NoError(t, err)
}

func TestHealthChecker_CheckApplicationHealth(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	hc := NewHealthChecker(clientset, nil, log)
	ctx := context.Background()

	// Should pass with empty namespace (no pods in self-healing-platform is ok)
	err := hc.CheckApplicationHealth(ctx)
	assert.NoError(t, err)
}

func TestHealthChecker_CheckNodesReady(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	hc := NewHealthChecker(clientset, nil, log)
	ctx := context.Background()

	// No nodes - should pass (empty cluster is valid)
	err := hc.checkNodesReady(ctx)
	assert.NoError(t, err)
}

func TestHealthChecker_CheckPodsRunning(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	hc := NewHealthChecker(clientset, nil, log)
	ctx := context.Background()

	// No pods in namespace - should pass
	err := hc.checkPodsRunning(ctx)
	assert.NoError(t, err)
}
