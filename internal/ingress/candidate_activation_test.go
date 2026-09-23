package ingress

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCandidateActivationRequiresExactReadyGeneration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		change     func(*appsv1.Deployment)
		wantActive bool
	}{
		{name: "ready", wantActive: true},
		{name: "old observation", change: func(d *appsv1.Deployment) { d.Status.ObservedGeneration = 0 }},
		{name: "partial rollout", change: func(d *appsv1.Deployment) { d.Status.UpdatedReplicas-- }},
		{name: "unavailable replica", change: func(d *appsv1.Deployment) { d.Status.AvailableReplicas-- }},
		{name: "changed image", change: func(d *appsv1.Deployment) { d.Spec.Template.Spec.Containers[0].Image = "untrusted:latest" }},
		{name: "deleted candidate", change: func(d *appsv1.Deployment) { now := metav1.Now(); d.DeletionTimestamp = &now }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := hostingTestConfig(t)
			id := strings.Repeat("a", 64)
			candidate, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", id, "")
			if err != nil {
				t.Fatal(err)
			}
			candidate.UID = "candidate-uid"
			candidate.Generation = 1
			n := *candidate.Spec.Replicas
			candidate.Status = appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: n, UpdatedReplicas: n, ReadyReplicas: n, AvailableReplicas: n}
			if tc.change != nil {
				tc.change(candidate)
			}
			policy, err := candidateNetworkPolicyForApp(cfg, DefaultNamespace, id)
			if err != nil {
				t.Fatal(err)
			}
			service := serviceForApp(cfg, DefaultNamespace)
			service.UID = "stable-service"
			service.ResourceVersion = "4"
			service.Annotations[activationOperationAnnotation] = id
			client := fake.NewSimpleClientset(candidate, policy, service)
			c := &Controller{namespace: DefaultNamespace, deployments: client.AppsV1().Deployments(DefaultNamespace), networkPolicies: client.NetworkingV1().NetworkPolicies(DefaultNamespace), services: client.CoreV1().Services(DefaultNamespace)}
			_, err = c.ActivatePreparedCandidate(context.Background(), activationGateFor(service), cfg, "", id, "")
			if (err == nil) != tc.wantActive {
				t.Fatalf("activation error: %v", err)
			}
			got, err := c.services.Get(context.Background(), cfg.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			active := got.Spec.Selector[candidateGenerationLabel] == candidateGeneration(cfg.Name, id)
			if active != tc.wantActive {
				t.Fatalf("unexpected selected generation: %#v", got.Spec.Selector)
			}
			if !tc.wantActive {
				for _, a := range client.Actions() {
					if a.GetVerb() == "update" || a.GetVerb() == "create" || a.GetVerb() == "delete" {
						t.Fatalf("unready candidate caused mutation: %s", a.GetVerb())
					}
				}
			}
		})
	}
}

func TestCandidateActivationRejectsWrongRequestBeforeRuntimeAccess(t *testing.T) {
	cfg := hostingTestConfig(t)
	c := &Controller{}
	_, err := c.ActivatePreparedCandidate(context.Background(), ActivationGate{App: cfg.Name, OperationID: strings.Repeat("b", 64)}, cfg, "", strings.Repeat("a", 64), "")
	if err != ErrActivationSuperseded {
		t.Fatalf("wrong request not rejected: %v", err)
	}
}
