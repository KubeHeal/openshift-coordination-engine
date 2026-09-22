package rca

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	coretesting "k8s.io/client-go/testing"
)

func istioReq() *Request {
	return &Request{
		Service:   "myapp",
		Namespace: "default",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	}
}

// fakeDiscoveryWithIstio returns a discovery client that reports Istio CRDs.
func fakeDiscoveryWithIstio() *fakediscovery.FakeDiscovery {
	fd := &fakediscovery.FakeDiscovery{
		Fake: &coretesting.Fake{},
	}
	fd.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "networking.istio.io/v1beta1",
			APIResources: []metav1.APIResource{
				{Name: "virtualservices", Kind: "VirtualService", Namespaced: true},
				{Name: "destinationrules", Kind: "DestinationRule", Namespaced: true},
			},
		},
	}
	return fd
}

// fakeDiscoveryWithoutIstio returns a discovery client with no Istio resources.
func fakeDiscoveryWithoutIstio() *fakediscovery.FakeDiscovery {
	fd := &fakediscovery.FakeDiscovery{
		Fake: &coretesting.Fake{},
	}
	fd.Resources = []*metav1.APIResourceList{}
	return fd
}

func makeVirtualService(httpRoutes []interface{}) *unstructured.Unstructured {
	vs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1beta1",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "my-vs",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"myapp"},
				"http":  httpRoutes,
			},
		},
	}
	return vs
}

func newDynamicClient(objs ...runtime.Object) *fakedynamic.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		vsGVR: "VirtualServiceList",
	}
	return fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objs...)
}

func TestIstioCorrelator_NotAvailable(t *testing.T) {
	disc := fakeDiscoveryWithoutIstio()
	dc := newDynamicClient()
	corr := NewIstioCorrelator(dc, disc, testLog())

	findings, err := corr.Correlate(istioReq())
	require.NoError(t, err)
	assert.Empty(t, findings)
	assert.False(t, corr.IsAvailable())
}

func TestIstioCorrelator_NoVirtualServices(t *testing.T) {
	disc := fakeDiscoveryWithIstio()
	dc := newDynamicClient()
	corr := NewIstioCorrelator(dc, disc, testLog())

	findings, err := corr.Correlate(istioReq())
	require.NoError(t, err)
	assert.Empty(t, findings)
	assert.True(t, corr.IsAvailable())
}

func TestIstioCorrelator_WeightMismatch(t *testing.T) {
	routes := []interface{}{
		map[string]interface{}{
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{"host": "myapp"},
					"weight":      int64(60),
				},
				map[string]interface{}{
					"destination": map[string]interface{}{"host": "myapp-canary"},
					"weight":      int64(30),
				},
			},
		},
	}
	vs := makeVirtualService(routes)

	disc := fakeDiscoveryWithIstio()
	dc := newDynamicClient(vs)
	corr := NewIstioCorrelator(dc, disc, testLog())

	findings, err := corr.Correlate(istioReq())
	require.NoError(t, err)
	require.NotEmpty(t, findings)

	hasWeight := false
	for _, f := range findings {
		if issue, ok := f.Evidence["issue"].(string); ok && issue == "weight_mismatch" {
			hasWeight = true
			assert.Contains(t, f.Description, "weights sum to 90")
		}
	}
	assert.True(t, hasWeight, "expected weight_mismatch finding")
}

func TestIstioCorrelator_ZeroWeight(t *testing.T) {
	routes := []interface{}{
		map[string]interface{}{
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{"host": "myapp"},
					"weight":      int64(0),
				},
				map[string]interface{}{
					"destination": map[string]interface{}{"host": "myapp-canary"},
					"weight":      int64(100),
				},
			},
		},
	}
	vs := makeVirtualService(routes)

	disc := fakeDiscoveryWithIstio()
	dc := newDynamicClient(vs)
	corr := NewIstioCorrelator(dc, disc, testLog())

	findings, err := corr.Correlate(istioReq())
	require.NoError(t, err)

	hasZero := false
	for _, f := range findings {
		if issue, ok := f.Evidence["issue"].(string); ok && issue == "zero_weight" {
			hasZero = true
			assert.Contains(t, f.Description, "0% traffic")
		}
	}
	assert.True(t, hasZero, "expected zero_weight finding")
}

func TestIstioCorrelator_NoHTTPRoutes(t *testing.T) {
	vs := makeVirtualService([]interface{}{})

	disc := fakeDiscoveryWithIstio()
	dc := newDynamicClient(vs)
	corr := NewIstioCorrelator(dc, disc, testLog())

	findings, err := corr.Correlate(istioReq())
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Description, "no HTTP routes")
}

func TestIstioCorrelator_NoDestinations(t *testing.T) {
	routes := []interface{}{
		map[string]interface{}{
			"route": []interface{}{},
		},
	}
	vs := makeVirtualService(routes)

	disc := fakeDiscoveryWithIstio()
	dc := newDynamicClient(vs)
	corr := NewIstioCorrelator(dc, disc, testLog())

	findings, err := corr.Correlate(istioReq())
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Description, "no destinations")
}

func TestIstioCorrelator_SkipsUnrelatedVS(t *testing.T) {
	vs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1beta1",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "other-vs",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"other-service"},
				"http":  []interface{}{},
			},
		},
	}

	disc := fakeDiscoveryWithIstio()
	dc := newDynamicClient(vs)
	corr := NewIstioCorrelator(dc, disc, testLog())

	findings, err := corr.Correlate(istioReq())
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestIstioCorrelator_NilDynamicClient(t *testing.T) {
	disc := fakeDiscoveryWithIstio()
	corr := NewIstioCorrelator(nil, disc, testLog())

	findings, err := corr.Correlate(istioReq())
	require.NoError(t, err)
	assert.Empty(t, findings)
}
