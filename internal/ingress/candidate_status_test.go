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

func TestStatusPathsNeverUseHealthyLegacyDeploymentForPendingCandidate(t *testing.T) {
	candidate, service, appName := readyCandidateStatusObjects(t)
	legacy := candidate.DeepCopy()
	legacy.Name = appName
	legacy.Labels = managedAppLabels(appName)
	legacy.Spec.Selector = &metav1.LabelSelector{MatchLabels: appLabels(appName)}
	legacy.Spec.Template.Labels = appLabels(appName)
	legacy.Status = appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2}
	service.Annotations[initialCandidateRequestAnnotation] = strings.Repeat("c", 64)
	delete(service.Annotations, activationOperationAnnotation)
	service.Spec.Selector = map[string]string{appOwnershipLabel: appName, candidateGenerationLabel: "inactive-generation"}
	client := fake.NewSimpleClientset(service, legacy)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	state, desired, available, err := controller.StatusDetails(context.Background(), appName)
	if err != nil || state != StatusUnavailable || desired != 0 || available != 0 {
		t.Fatalf("pending candidate fell back to legacy deployment: state=%q desired=%d available=%d err=%v", state, desired, available, err)
	}
}

func TestRuntimeStatusListsPodsForSelectedCandidateOnly(t *testing.T) {
	candidate, service, appName := readyCandidateStatusObjects(t)
	candidate.Status.AvailableReplicas = 2
	legacyPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: DefaultNamespace, Labels: appLabels(appName)}, Status: corev1.PodStatus{Phase: corev1.PodRunning}, Spec: corev1.PodSpec{NodeName: "legacy-node"}}
	candidatePod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "candidate", Namespace: DefaultNamespace, Labels: candidate.Spec.Template.Labels}, Status: corev1.PodStatus{Phase: corev1.PodRunning}, Spec: corev1.PodSpec{NodeName: "candidate-node"}}
	client := fake.NewSimpleClientset(service, candidate, legacyPod, candidatePod)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace), pods: client.CoreV1().Pods(DefaultNamespace)}
	state, _, _, nodes, err := controller.RuntimeStatus(context.Background(), appName)
	if err != nil || state != StatusHealthy || len(nodes) != 1 || nodes[0] != "candidate-node" {
		t.Fatalf("runtime status selected wrong pods: state=%q nodes=%v err=%v", state, nodes, err)
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

func TestActivatedBootstrapReportsSelectedCandidate(t *testing.T) {
	candidate, service, app := readyCandidateStatusObjects(t)
	service.Annotations[initialCandidateRequestAnnotation] = service.Annotations[activationOperationAnnotation]
	client := fake.NewSimpleClientset(candidate, service)
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	state, err := c.Status(context.Background(), app)
	if err != nil || state != StatusHealthy {
		t.Fatalf("activated bootstrap not healthy: %s %v", state, err)
	}
}
