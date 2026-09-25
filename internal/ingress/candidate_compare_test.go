package ingress

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestCandidateDeploymentMatchesKubernetesDefaults(t *testing.T) {
	cfg := hostingTestConfig(t)
	desired, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", strings.Repeat("a", 64), "")
	if err != nil {
		t.Fatalf("render candidate: %v", err)
	}
	existing := desired.DeepCopy()
	normalizeCandidateDeployment(existing)
	if !candidateDeploymentMatches(existing, desired) {
		t.Fatal("known Kubernetes defaults should compare equal")
	}
	existing.Spec.Template.Spec.Containers[0].Image = "example.invalid/changed:1"
	if candidateDeploymentMatches(existing, desired) {
		t.Fatal("changed image compared equal")
	}
}

func TestCandidateDeploymentMatchesRejectsProtectedChanges(t *testing.T) {
	cfg := hostingTestConfig(t)
	desired, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", strings.Repeat("b", 64), "")
	if err != nil {
		t.Fatalf("render candidate: %v", err)
	}
	checks := []func(*appsv1.Deployment){
		func(d *appsv1.Deployment) { d.Labels[candidateGenerationLabel] = "different" },
		func(d *appsv1.Deployment) { d.Spec.Selector.MatchLabels[candidateGenerationLabel] = "different" },
		func(d *appsv1.Deployment) { d.Spec.Template.Labels[candidateGenerationLabel] = "different" },
		func(d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers = append(d.Spec.Template.Spec.Containers, corev1.Container{Name: "extra", Image: "extra:1"})
		},
	}
	for i, mutate := range checks {
		existing := desired.DeepCopy()
		mutate(existing)
		if candidateDeploymentMatches(existing, desired) {
			t.Fatalf("protected mutation %d compared equal", i)
		}
	}
	withoutSelector := desired.DeepCopy()
	withoutSelector.Spec.Selector = nil
	if candidateDeploymentMatches(withoutSelector, desired) {
		t.Fatal("nil selector compared equal")
	}
}

func TestCandidateNetworkPolicyMatchesProtocolDefaults(t *testing.T) {
	cfg := hostingTestConfig(t)
	desired, err := candidateNetworkPolicyForApp(cfg, DefaultNamespace, strings.Repeat("c", 64))
	if err != nil {
		t.Fatalf("render policy: %v", err)
	}
	existing := desired.DeepCopy()
	for i := range existing.Spec.Ingress {
		for j := range existing.Spec.Ingress[i].Ports {
			if existing.Spec.Ingress[i].Ports[j].Protocol != nil && *existing.Spec.Ingress[i].Ports[j].Protocol == corev1.ProtocolTCP {
				existing.Spec.Ingress[i].Ports[j].Protocol = nil
			}
		}
	}
	for i := range existing.Spec.Egress {
		for j := range existing.Spec.Egress[i].Ports {
			if existing.Spec.Egress[i].Ports[j].Protocol != nil && *existing.Spec.Egress[i].Ports[j].Protocol == corev1.ProtocolTCP {
				existing.Spec.Egress[i].Ports[j].Protocol = nil
			}
		}
	}
	if !candidateNetworkPolicyMatches(existing, desired) {
		t.Fatal("default TCP protocol should compare equal")
	}
	existing.Spec.PodSelector.MatchLabels[candidateGenerationLabel] = "different"
	if candidateNetworkPolicyMatches(existing, desired) {
		t.Fatal("changed policy selector compared equal")
	}
}

func TestCandidateMatchesEmptyAPISecurityContext(t *testing.T) {
	cfg := hostingTestConfig(t)
	desired, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", strings.Repeat("d", 64), "")
	if err != nil {
		t.Fatal(err)
	}
	desired.Spec.Template.Spec.SecurityContext = nil
	observed := desired.DeepCopy()
	observed.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{}
	if !candidateDeploymentMatches(observed, desired) {
		t.Fatal("empty API security context rejected")
	}
	observed.Spec.Template.Spec.SecurityContext.RunAsUser = int64Ptr(0)
	if candidateDeploymentMatches(observed, desired) {
		t.Fatal("explicit security change accepted")
	}
}

func TestCandidateServicePortsAcceptOnlyDefaultProtocol(t *testing.T) {
	expected := []corev1.ServicePort{{Name: "http", Port: 8080}}
	actual := append([]corev1.ServicePort(nil), expected...)
	actual[0].Protocol = corev1.ProtocolTCP
	if !candidateServicePortsMatch(actual, expected) {
		t.Fatal("API TCP default rejected")
	}
	actual[0].Protocol = corev1.ProtocolUDP
	if candidateServicePortsMatch(actual, expected) {
		t.Fatal("UDP accepted as TCP")
	}
	actual[0].Protocol = corev1.ProtocolTCP
	actual[0].Port = 8081
	if candidateServicePortsMatch(actual, expected) {
		t.Fatal("port change accepted")
	}
}
