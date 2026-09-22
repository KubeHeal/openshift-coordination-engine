package rca

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newReq() *Request {
	return &Request{
		Service:   "myapp",
		Namespace: "default",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	}
}

func servicePod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{"app": "myapp"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestNetpolCorrelator_DenyAllIngress(t *testing.T) {
	pod := servicePod("myapp-abc")
	netpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{}, // empty = deny all
		},
	}

	clientset := fake.NewSimpleClientset(pod, netpol)
	corr := NewNetpolCorrelator(clientset, testLog())

	findings, err := corr.Correlate(newReq())
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, SignalNetworkPolicy, findings[0].SignalType)
	assert.Contains(t, findings[0].Description, "denies all ingress")
	assert.Equal(t, 0.90, findings[0].Confidence)
}

func TestNetpolCorrelator_DenyAllEgress(t *testing.T) {
	pod := servicePod("myapp-xyz")
	netpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "block-egress", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      []networkingv1.NetworkPolicyEgressRule{},
		},
	}

	clientset := fake.NewSimpleClientset(pod, netpol)
	corr := NewNetpolCorrelator(clientset, testLog())

	findings, err := corr.Correlate(newReq())
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Description, "denies all egress")
}

func TestNetpolCorrelator_RestrictiveIngress(t *testing.T) {
	pod := servicePod("myapp-abc")
	netpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "narrow-ingress", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "frontend"}}},
					},
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(pod, netpol)
	corr := NewNetpolCorrelator(clientset, testLog())

	findings, err := corr.Correlate(newReq())
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Description, "restrictive ingress")
}

func TestNetpolCorrelator_NoFindings_WhenPolicyIsPermissive(t *testing.T) {
	pod := servicePod("myapp-abc")
	netpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-all", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "a"}}},
						{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "b"}}},
						{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "c"}}},
					},
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(pod, netpol)
	corr := NewNetpolCorrelator(clientset, testLog())

	findings, err := corr.Correlate(newReq())
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestNetpolCorrelator_NoPods(t *testing.T) {
	netpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{},
		},
	}

	clientset := fake.NewSimpleClientset(netpol)
	corr := NewNetpolCorrelator(clientset, testLog())

	findings, err := corr.Correlate(newReq())
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestNetpolCorrelator_PolicyDoesNotSelectService(t *testing.T) {
	pod := servicePod("myapp-abc")
	netpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "other-policy", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "other"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{},
		},
	}

	clientset := fake.NewSimpleClientset(pod, netpol)
	corr := NewNetpolCorrelator(clientset, testLog())

	findings, err := corr.Correlate(newReq())
	require.NoError(t, err)
	// Should detect that the pod is not selected by any policy.
	hasDefault := false
	for _, f := range findings {
		if ev, ok := f.Evidence["effect"].(string); ok && ev == "default_policy" {
			hasDefault = true
		}
	}
	assert.True(t, hasDefault, "expected default_policy finding for unselected pods")
}
