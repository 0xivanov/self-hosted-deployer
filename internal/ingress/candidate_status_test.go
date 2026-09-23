package ingress

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func readyCandidateStatusObjects(t *testing.T) (*appsv1.Deployment, *corev1.Service, string) {
	t.Helper()
	cfg := hostingTestConfig(t)
	requestID := strings.Repeat("a", 64)
	candidate, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", requestID, "")
	if err != nil {
		t.Fatal(err)
	}
	candidate.UID = "candidate-uid"
	candidate.Generation = 1
	replicas := *candidate.Spec.Replicas
	candidate.Status = appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: replicas, UpdatedReplicas: replicas, ReadyReplicas: replicas, AvailableReplicas: replicas}
	service := serviceForApp(cfg, DefaultNamespace)
	service.UID = "service-uid"
	service.ResourceVersion = "7"
	service.Annotations[activationOperationAnnotation] = requestID
	service.Spec.Selector = map[string]string{"deployer.io/app": cfg.Name, candidateGenerationLabel: candidate.Labels[candidateGenerationLabel]}
	return candidate, service, cfg.Name
}

func TestCandidateStatusUsesSelectedDeploymentOnly(t *testing.T) {
	candidate, service, appName := readyCandidateStatusObjects(t)
	client := fake.NewSimpleClientset(candidate, service)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	status, err := controller.CandidateStatus(context.Background(), appName)
	if err != nil {
		t.Fatalf("candidate status: %v", err)
	}
	wantReplicas := *candidate.Spec.Replicas
	if status.State != StatusHealthy || status.DesiredReplicas != wantReplicas || status.AvailableReplicas != wantReplicas || status.DeploymentName != candidate.Name {
		t.Fatalf("unexpected candidate status: %#v", status)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" && action.GetVerb() != "list" {
			t.Fatalf("candidate status mutated Kubernetes: %s", action.GetVerb())
		}
	}
}

func TestCandidateStatusUsesServiceGenerationAcrossRecoveryOperation(t *testing.T) {
	candidate, service, appName := readyCandidateStatusObjects(t)
	service.Annotations[activationOperationAnnotation] = strings.Repeat("b", 64)
	client := fake.NewSimpleClientset(candidate, service)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	got, err := controller.CandidateStatus(context.Background(), appName)
	if err != nil || got.DeploymentName != candidate.Name || got.State != StatusHealthy {
		t.Fatalf("recovery operation changed selected candidate: status=%#v err=%v", got, err)
	}
}

func TestCandidateStatusReturnsLegacyFallbackOnlyForUnannotatedLegacyService(t *testing.T) {
	_, service, appName := readyCandidateStatusObjects(t)
	delete(service.Annotations, activationOperationAnnotation)
	service.Spec.Selector = map[string]string{appOwnershipLabel: appName}
	client := fake.NewSimpleClientset(service)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	if _, err := controller.CandidateStatus(context.Background(), appName); !errors.Is(err, ErrCandidateStatusNotSelected) {
		t.Fatalf("legacy service did not return explicit fallback sentinel: %v", err)
	}
}

func TestCandidateStatusRejectsStaleOrUnsafeSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsv1.Deployment, *corev1.Service)
		state  string
	}{
		{name: "stale observation", change: func(d *appsv1.Deployment, _ *corev1.Service) { d.Status.ObservedGeneration = 0 }, state: StatusDegraded},
		{name: "extra replica", change: func(d *appsv1.Deployment, _ *corev1.Service) { d.Status.Replicas++ }, state: StatusDegraded},
		{name: "unavailable replica", change: func(d *appsv1.Deployment, _ *corev1.Service) { d.Status.UnavailableReplicas = 1 }, state: StatusDegraded},
		{name: "deleting", change: func(d *appsv1.Deployment, _ *corev1.Service) { now := metav1.Now(); d.DeletionTimestamp = &now }, state: StatusUnavailable},
		{name: "selector drift", change: func(_ *appsv1.Deployment, s *corev1.Service) { s.Spec.Selector[candidateGenerationLabel] = "wrong" }, state: "error"},
		{name: "invalid activation", change: func(_ *appsv1.Deployment, s *corev1.Service) { s.Annotations[activationOperationAnnotation] = "legacy" }, state: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, service, appName := readyCandidateStatusObjects(t)
			tc.change(candidate, service)
			client := fake.NewSimpleClientset(candidate, service)
			controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
			got, err := controller.CandidateStatus(context.Background(), appName)
			if tc.state == "error" {
				if err == nil || errors.Is(err, ErrCandidateStatusNotSelected) {
					t.Fatalf("expected candidate status failure, got status=%#v err=%v", got, err)
				}
				return
			}
			if err != nil || got.State != tc.state {
				t.Fatalf("status=%#v err=%v, want %s", got, err, tc.state)
			}
		})
	}
}

func TestCandidateStatusRejectsForeignOrAmbiguousDeployments(t *testing.T) {
	candidate, service, appName := readyCandidateStatusObjects(t)
	foreign := candidate.DeepCopy()
	foreign.Name = "foreign-candidate"
	foreign.Labels = map[string]string{"deployer.io/app": appName, candidateGenerationLabel: candidate.Labels[candidateGenerationLabel], managedByLabel: "other-controller"}
	client := fake.NewSimpleClientset(candidate, foreign, service)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	if _, err := controller.CandidateStatus(context.Background(), appName); err == nil || errors.Is(err, ErrCandidateStatusNotSelected) {
		t.Fatalf("foreign or ambiguous candidate accepted: %v", err)
	}
}
