package rca

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// NetpolCorrelator inspects NetworkPolicies that may be blocking traffic
// to/from the target service.
type NetpolCorrelator struct {
	clientset kubernetes.Interface
	log       *logrus.Logger
}

// NewNetpolCorrelator creates a new NetworkPolicy correlator.
func NewNetpolCorrelator(clientset kubernetes.Interface, log *logrus.Logger) *NetpolCorrelator {
	return &NetpolCorrelator{clientset: clientset, log: log}
}

// Name returns the correlator identifier.
func (nc *NetpolCorrelator) Name() string { return "network_policy" }

// Correlate lists NetworkPolicies and pods in the namespace, then detects
// policies that could be blocking traffic for the target service.
func (nc *NetpolCorrelator) Correlate(req *Request) ([]RootCause, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	policies, err := nc.clientset.NetworkingV1().NetworkPolicies(req.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list NetworkPolicies in namespace %s: %w", req.Namespace, err)
	}

	pods, err := nc.clientset.CoreV1().Pods(req.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods in namespace %s: %w", req.Namespace, err)
	}

	servicePods := filterServicePods(pods.Items, req.Service)
	if len(servicePods) == 0 {
		nc.log.WithFields(logrus.Fields{
			"namespace": req.Namespace,
			"service":   req.Service,
		}).Debug("No pods found for service, skipping NetworkPolicy analysis")
		return nil, nil
	}

	var findings []RootCause

	for i := range policies.Items {
		pol := &policies.Items[i]
		matched := matchingPods(pol, servicePods)
		if len(matched) == 0 {
			continue
		}

		if f := nc.checkDenyAllIngress(pol, matched); f != nil {
			findings = append(findings, *f)
		}
		if f := nc.checkDenyAllEgress(pol, matched); f != nil {
			findings = append(findings, *f)
		}
		if f := nc.checkRestrictiveIngress(pol, matched); f != nil {
			findings = append(findings, *f)
		}
	}

	// Check for no-policy-at-all scenario (pods have no policy selecting them).
	if len(findings) == 0 && len(policies.Items) > 0 {
		orphaned := nc.findUnselectedPods(policies.Items, servicePods)
		if len(orphaned) > 0 {
			findings = append(findings, RootCause{
				SignalType:  SignalNetworkPolicy,
				Description: fmt.Sprintf("No NetworkPolicy selects %d service pod(s) — default cluster policy applies", len(orphaned)),
				Confidence:  0.40,
				Evidence: map[string]interface{}{
					"effect":       "default_policy",
					"matched_pods": len(orphaned),
					"pod_names":    podNames(orphaned),
				},
				RemediationSteps: []string{
					"Verify that the cluster default network policy permits required traffic",
					"Create an explicit NetworkPolicy for the service if needed",
				},
			})
		}
	}

	nc.log.WithFields(logrus.Fields{
		"namespace":    req.Namespace,
		"service":      req.Service,
		"policies":     len(policies.Items),
		"service_pods": len(servicePods),
		"findings":     len(findings),
	}).Info("NetworkPolicy correlation complete")

	return findings, nil
}

// checkDenyAllIngress detects a policy that selects the service pods but has
// an empty ingress rule list (implicit deny-all ingress).
func (nc *NetpolCorrelator) checkDenyAllIngress(pol *networkingv1.NetworkPolicy, matched []corev1.Pod) *RootCause {
	if !hasPolicyType(pol, networkingv1.PolicyTypeIngress) {
		return nil
	}
	if len(pol.Spec.Ingress) > 0 {
		return nil
	}
	return &RootCause{
		SignalType:  SignalNetworkPolicy,
		Description: fmt.Sprintf("NetworkPolicy '%s' denies all ingress to %d service pod(s)", pol.Name, len(matched)),
		Confidence:  0.90,
		Evidence: map[string]interface{}{
			"policy_name":  pol.Name,
			"effect":       "deny_ingress",
			"matched_pods": len(matched),
			"pod_names":    podNames(matched),
		},
		RemediationSteps: []string{
			fmt.Sprintf("Add an ingress rule to NetworkPolicy '%s' allowing required source traffic", pol.Name),
			"Or create a separate NetworkPolicy with ingress allow rules for the service",
		},
	}
}

// checkDenyAllEgress detects a policy that denies all egress.
func (nc *NetpolCorrelator) checkDenyAllEgress(pol *networkingv1.NetworkPolicy, matched []corev1.Pod) *RootCause {
	if !hasPolicyType(pol, networkingv1.PolicyTypeEgress) {
		return nil
	}
	if len(pol.Spec.Egress) > 0 {
		return nil
	}
	return &RootCause{
		SignalType:  SignalNetworkPolicy,
		Description: fmt.Sprintf("NetworkPolicy '%s' denies all egress from %d service pod(s)", pol.Name, len(matched)),
		Confidence:  0.88,
		Evidence: map[string]interface{}{
			"policy_name":  pol.Name,
			"effect":       "deny_egress",
			"matched_pods": len(matched),
			"pod_names":    podNames(matched),
		},
		RemediationSteps: []string{
			fmt.Sprintf("Add egress rules to NetworkPolicy '%s' allowing required destinations", pol.Name),
			"Ensure DNS egress (port 53) is permitted for service discovery",
		},
	}
}

// checkRestrictiveIngress flags policies with very narrow ingress that might
// accidentally exclude expected callers.
func (nc *NetpolCorrelator) checkRestrictiveIngress(pol *networkingv1.NetworkPolicy, matched []corev1.Pod) *RootCause {
	if !hasPolicyType(pol, networkingv1.PolicyTypeIngress) {
		return nil
	}
	if len(pol.Spec.Ingress) == 0 {
		return nil // already handled by deny-all check
	}

	// Count total allowed sources across all ingress rules.
	totalSources := 0
	for _, rule := range pol.Spec.Ingress {
		totalSources += len(rule.From)
	}
	if totalSources > 2 {
		return nil // liberal enough — not suspicious
	}

	confidence := 0.60
	if totalSources == 0 {
		// Ingress rules with no `from` means "allow all" — not restrictive.
		return nil
	}

	return &RootCause{
		SignalType:  SignalNetworkPolicy,
		Description: fmt.Sprintf("NetworkPolicy '%s' has restrictive ingress (%d source(s)) for %d service pod(s)", pol.Name, totalSources, len(matched)),
		Confidence:  math.Round(confidence*100) / 100,
		Evidence: map[string]interface{}{
			"policy_name":     pol.Name,
			"effect":          "restrictive_ingress",
			"ingress_sources": totalSources,
			"matched_pods":    len(matched),
		},
		RemediationSteps: []string{
			"Verify that all expected client namespaces/pods are listed in the ingress rules",
			fmt.Sprintf("Review NetworkPolicy '%s' ingress.from selectors", pol.Name),
		},
	}
}

// findUnselectedPods returns pods not selected by any NetworkPolicy.
func (nc *NetpolCorrelator) findUnselectedPods(policies []networkingv1.NetworkPolicy, servicePods []corev1.Pod) []corev1.Pod {
	var orphaned []corev1.Pod
	for i := range servicePods {
		selected := false
		for j := range policies {
			sel, err := metav1.LabelSelectorAsSelector(&policies[j].Spec.PodSelector)
			if err != nil {
				continue
			}
			if sel.Matches(labels.Set(servicePods[i].Labels)) {
				selected = true
				break
			}
		}
		if !selected {
			orphaned = append(orphaned, servicePods[i])
		}
	}
	return orphaned
}

// --- helpers ---

// filterServicePods returns pods whose name starts with "<service>-".
func filterServicePods(pods []corev1.Pod, service string) []corev1.Pod {
	prefix := service + "-"
	var out []corev1.Pod
	for i := range pods {
		if strings.HasPrefix(pods[i].Name, prefix) || pods[i].Name == service {
			out = append(out, pods[i])
		}
	}
	return out
}

// matchingPods returns the subset of pods whose labels match the policy's podSelector.
func matchingPods(pol *networkingv1.NetworkPolicy, pods []corev1.Pod) []corev1.Pod {
	sel, err := metav1.LabelSelectorAsSelector(&pol.Spec.PodSelector)
	if err != nil {
		return nil
	}
	var out []corev1.Pod
	for i := range pods {
		if sel.Matches(labels.Set(pods[i].Labels)) {
			out = append(out, pods[i])
		}
	}
	return out
}

func hasPolicyType(pol *networkingv1.NetworkPolicy, pt networkingv1.PolicyType) bool {
	for _, t := range pol.Spec.PolicyTypes {
		if t == pt {
			return true
		}
	}
	return false
}

func podNames(pods []corev1.Pod) []string {
	names := make([]string, len(pods))
	for i := range pods {
		names[i] = pods[i].Name
	}
	return names
}
