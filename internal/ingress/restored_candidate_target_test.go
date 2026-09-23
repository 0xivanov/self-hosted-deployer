package ingress

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRestoredCandidateTargetReadyRequiresExactSelectedWorkload(t *testing.T) {
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
	service.Spec.Selector = maps.Clone(candidate.Spec.Selector.MatchLabels)
	target := ActivationTarget{Selector: maps.Clone(service.Spec.Selector), Ports: append([]corev1.ServicePort(nil), service.Spec.Ports...)}
	client := fake.NewSimpleClientset(service, candidate)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	gate := activationGateFor(service)
	ready, err := controller.RestoredCandidateTargetReady(context.Background(), gate, target, cfg)
	if err != nil || !ready {
		t.Fatalf("restored candidate target: ready=%v err=%v", ready, err)
	}
	candidate.Spec.Template.Spec.Containers[0].Image = "tampered:latest"
	client = fake.NewSimpleClientset(service, candidate)
	controller.deployments = client.AppsV1().Deployments(DefaultNamespace)
	if _, err := controller.RestoredCandidateTargetReady(context.Background(), gate, target, cfg); err == nil {
		t.Fatal("tampered candidate accepted")
	}
}

func TestRestoredCandidateTargetReadyAllowsRecoveryOperationButUsesTargetGeneration(t *testing.T) {
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
	service.Annotations[activationOperationAnnotation] = strings.Repeat("b", 64)
	service.Spec.Selector = maps.Clone(candidate.Spec.Selector.MatchLabels)
	target := ActivationTarget{Selector: maps.Clone(service.Spec.Selector), Ports: append([]corev1.ServicePort(nil), service.Spec.Ports...)}
	client := fake.NewSimpleClientset(service, candidate)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	ready, err := controller.RestoredCandidateTargetReady(context.Background(), activationGateFor(service), target, cfg)
	if err != nil || !ready {
		t.Fatalf("recovery operation incorrectly changed target: ready=%v err=%v", ready, err)
	}
}

func TestRestoredLegacyTargetRejectsMutableSecretsAndChecksReadiness(t *testing.T) {
	cfg := hostingTestConfig(t)
	legacy, err := deploymentForApp(cfg, DefaultNamespace, "")
	if err != nil {
		t.Fatal(err)
	}
	legacy.UID = "legacy-uid"
	legacy.Generation = 1
	replicas := *legacy.Spec.Replicas
	legacy.Status = appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: replicas, UpdatedReplicas: replicas, ReadyReplicas: replicas, AvailableReplicas: replicas}
	service := serviceForApp(cfg, DefaultNamespace)
	service.UID = "service-uid"
	service.ResourceVersion = "7"
	service.Annotations[activationOperationAnnotation] = strings.Repeat("c", 64)
	target := ActivationTarget{Selector: maps.Clone(service.Spec.Selector), Ports: append([]corev1.ServicePort(nil), service.Spec.Ports...)}
	client := fake.NewSimpleClientset(service, legacy)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	ready, err := controller.RestoredCandidateTargetReady(context.Background(), activationGateFor(service), target, cfg)
	if err != nil || !ready {
		t.Fatalf("legacy target: ready=%v err=%v", ready, err)
	}
	cfg.Secrets = []string{"TOKEN"}
	if _, err := controller.RestoredCandidateTargetReady(context.Background(), activationGateFor(service), target, cfg); err == nil {
		t.Fatal("mutable legacy secret target accepted")
	}
	legacy.Status.ObservedGeneration = 0
	client = fake.NewSimpleClientset(service, legacy)
	controller.deployments = client.AppsV1().Deployments(DefaultNamespace)
	ready, err = controller.RestoredCandidateTargetReady(context.Background(), activationGateFor(service), target, hostingTestConfig(t))
	if err != nil || ready {
		t.Fatalf("stale legacy target: ready=%v err=%v", ready, err)
	}
}

func TestRestoredCandidateTargetReadyRejectsGateOrTargetDrift(t *testing.T) {
	cfg := hostingTestConfig(t)
	legacy, err := deploymentForApp(cfg, DefaultNamespace, "")
	if err != nil {
		t.Fatal(err)
	}
	legacy.UID = "legacy-uid"
	legacy.Generation = 1
	service := serviceForApp(cfg, DefaultNamespace)
	service.UID = "service-uid"
	service.ResourceVersion = "7"
	service.Annotations[activationOperationAnnotation] = strings.Repeat("d", 64)
	client := fake.NewSimpleClientset(service, legacy)
	controller := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), deployments: client.AppsV1().Deployments(DefaultNamespace)}
	target := ActivationTarget{Selector: map[string]string{"deployer.io/app": cfg.Name}, Ports: service.Spec.Ports}
	_, err = controller.RestoredCandidateTargetReady(context.Background(), ActivationGate{App: cfg.Name, Namespace: DefaultNamespace, UID: "wrong", ResourceVersion: "7", OperationID: service.Annotations[activationOperationAnnotation]}, target, cfg)
	if !errors.Is(err, ErrActivationSuperseded) {
		t.Fatalf("gate drift accepted: %v", err)
	}
}
