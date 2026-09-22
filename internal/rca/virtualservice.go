package rca

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

var vsGVR = schema.GroupVersionResource{
	Group:    "networking.istio.io",
	Version:  "v1beta1",
	Resource: "virtualservices",
}

// IstioCorrelator inspects Istio VirtualService resources for routing
// misconfigurations that could affect the target service.
type IstioCorrelator struct {
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	log             *logrus.Logger

	istioOnce      sync.Once
	istioAvailable bool
}

// NewIstioCorrelator creates a new Istio VirtualService correlator.
// discoveryClient is used to check whether Istio CRDs are installed.
func NewIstioCorrelator(
	dynamicClient dynamic.Interface,
	discoveryClient discovery.DiscoveryInterface,
	log *logrus.Logger,
) *IstioCorrelator {
	return &IstioCorrelator{
		dynamicClient:   dynamicClient,
		discoveryClient: discoveryClient,
		log:             log,
	}
}

// Name returns the correlator identifier.
func (ic *IstioCorrelator) Name() string { return "istio_virtual_service" }

// IsAvailable returns whether Istio CRDs were detected. The result is cached
// after the first check.
func (ic *IstioCorrelator) IsAvailable() bool {
	ic.istioOnce.Do(func() {
		ic.istioAvailable = ic.detectIstio()
	})
	return ic.istioAvailable
}

// Correlate lists VirtualServices in the namespace and detects
// misconfigurations related to the target service.  Returns nil (no error)
// when Istio is not installed.
func (ic *IstioCorrelator) Correlate(req *Request) ([]RootCause, error) {
	if !ic.IsAvailable() {
		ic.log.Debug("Istio CRDs not detected — skipping VirtualService correlation")
		return nil, nil
	}

	if ic.dynamicClient == nil {
		ic.log.Debug("Dynamic client not available — skipping VirtualService correlation")
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	vsList, err := ic.dynamicClient.Resource(vsGVR).Namespace(req.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list VirtualServices in namespace %s: %w", req.Namespace, err)
	}

	var findings []RootCause

	for i := range vsList.Items {
		vs := &vsList.Items[i]
		if !ic.referencesService(vs, req.Service) {
			continue
		}

		findings = append(findings, ic.analyzeVirtualService(vs, req.Service)...)
	}

	ic.log.WithFields(logrus.Fields{
		"namespace":        req.Namespace,
		"service":          req.Service,
		"virtual_services": len(vsList.Items),
		"findings":         len(findings),
	}).Info("Istio VirtualService correlation complete")

	return findings, nil
}

// detectIstio uses the discovery API to check for Istio networking CRDs.
func (ic *IstioCorrelator) detectIstio() bool {
	if ic.discoveryClient == nil {
		return false
	}
	_, err := ic.discoveryClient.ServerResourcesForGroupVersion("networking.istio.io/v1beta1")
	if err != nil {
		ic.log.WithError(err).Debug("Istio CRD group not found via discovery API")
		return false
	}
	ic.log.Info("Istio CRDs detected via discovery API")
	return true
}

// referencesService returns true if the VirtualService has any HTTP route
// destination referencing the target service host.
func (ic *IstioCorrelator) referencesService(vs *unstructured.Unstructured, service string) bool {
	hosts, _, err := unstructured.NestedStringSlice(vs.Object, "spec", "hosts")
	if err == nil {
		for _, h := range hosts {
			if h == service || strings.HasPrefix(h, service+".") {
				return true
			}
		}
	}

	httpRoutes, _, err := unstructured.NestedSlice(vs.Object, "spec", "http")
	if err != nil {
		return false
	}
	for _, r := range httpRoutes {
		route, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		routeDests, _, routeErr := unstructured.NestedSlice(route, "route")
		if routeErr != nil {
			continue
		}
		for _, rd := range routeDests {
			dest, ok := rd.(map[string]interface{})
			if !ok {
				continue
			}
			host, _, hostErr := unstructured.NestedString(dest, "destination", "host")
			if hostErr != nil {
				continue
			}
			if host == service || strings.HasPrefix(host, service+".") {
				return true
			}
		}
	}
	return false
}

// analyzeVirtualService inspects a single VirtualService for issues.
func (ic *IstioCorrelator) analyzeVirtualService(vs *unstructured.Unstructured, service string) []RootCause {
	vsName := vs.GetName()
	var findings []RootCause

	httpRoutes, _, httpErr := unstructured.NestedSlice(vs.Object, "spec", "http")
	if httpErr != nil || len(httpRoutes) == 0 {
		findings = append(findings, RootCause{
			SignalType:  SignalIstioVS,
			Description: fmt.Sprintf("VirtualService '%s' has no HTTP routes defined", vsName),
			Confidence:  0.75,
			Evidence: map[string]interface{}{
				"virtual_service": vsName,
				"issue":           "no_http_routes",
			},
			RemediationSteps: []string{
				fmt.Sprintf("Add HTTP route configuration to VirtualService '%s'", vsName),
			},
		})
		return findings
	}

	for idx, r := range httpRoutes {
		route, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		findings = append(findings, ic.checkRoute(vsName, service, route, idx)...)
	}

	return findings
}

// checkRoute inspects a single HTTP route for weight and destination issues.
func (ic *IstioCorrelator) checkRoute(vsName, service string, route map[string]interface{}, routeIdx int) []RootCause {
	var findings []RootCause

	routeDests, _, rdErr := unstructured.NestedSlice(route, "route")
	if rdErr != nil || len(routeDests) == 0 {
		findings = append(findings, RootCause{
			SignalType:  SignalIstioVS,
			Description: fmt.Sprintf("VirtualService '%s' route[%d] has no destinations", vsName, routeIdx),
			Confidence:  0.85,
			Evidence: map[string]interface{}{
				"virtual_service": vsName,
				"route_index":     routeIdx,
				"issue":           "no_destinations",
			},
			RemediationSteps: []string{
				fmt.Sprintf("Add destination(s) to VirtualService '%s' route[%d]", vsName, routeIdx),
			},
		})
		return findings
	}

	// Check weight sum.
	var totalWeight int64
	hasWeights := false
	for _, rd := range routeDests {
		dest, ok := rd.(map[string]interface{})
		if !ok {
			continue
		}
		w, found, wErr := unstructured.NestedInt64(dest, "weight")
		if wErr == nil && found {
			hasWeights = true
			totalWeight += w
		}
	}

	if hasWeights && totalWeight != 100 {
		confidence := 0.88
		if totalWeight == 0 {
			confidence = 0.92
		}
		findings = append(findings, RootCause{
			SignalType:  SignalIstioVS,
			Description: fmt.Sprintf("VirtualService '%s' route[%d] weights sum to %d (expected 100)", vsName, routeIdx, totalWeight),
			Confidence:  math.Round(confidence*100) / 100,
			Evidence: map[string]interface{}{
				"virtual_service": vsName,
				"route_index":     routeIdx,
				"issue":           "weight_mismatch",
				"total_weight":    totalWeight,
			},
			RemediationSteps: []string{
				fmt.Sprintf("Adjust route weights in VirtualService '%s' route[%d] to sum to 100", vsName, routeIdx),
			},
		})
	}

	findings = append(findings, ic.checkZeroWeight(vsName, service, routeDests, routeIdx)...)

	return findings
}

// checkZeroWeight detects destinations that send 0% traffic to the service.
func (ic *IstioCorrelator) checkZeroWeight(vsName, service string, routeDests []interface{}, routeIdx int) []RootCause {
	var findings []RootCause
	for _, rd := range routeDests {
		dest, ok := rd.(map[string]interface{})
		if !ok {
			continue
		}
		host, _, hErr := unstructured.NestedString(dest, "destination", "host")
		if hErr != nil {
			continue
		}
		if host != service && !strings.HasPrefix(host, service+".") {
			continue
		}
		w, found, _ := unstructured.NestedInt64(dest, "weight") //nolint:errcheck // zero value on error is safe
		if found && w == 0 {
			findings = append(findings, RootCause{
				SignalType:  SignalIstioVS,
				Description: fmt.Sprintf("VirtualService '%s' route[%d] sends 0%% traffic to service '%s'", vsName, routeIdx, service),
				Confidence:  0.92,
				Evidence: map[string]interface{}{
					"virtual_service": vsName,
					"route_index":     routeIdx,
					"issue":           "zero_weight",
					"destination":     host,
				},
				RemediationSteps: []string{
					fmt.Sprintf("Increase weight for destination '%s' in VirtualService '%s'", service, vsName),
				},
			})
		}
	}
	return findings
}
