package ingress

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCandidateDeploymentIsolatedFromStableServiceSelector(t *testing.T) {
	cfg := hostingTestConfig(t)
	requestID := strings.Repeat("a", 64)
	deployment, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", requestID, "")
	if err != nil {
		t.Fatalf("render candidate: %v", err)
	}
	if len(deployment.Name) > 63 || deployment.Name == cfg.Name {
		t.Fatalf("invalid candidate name %q", deployment.Name)
	}
	if deployment.Labels[candidateGenerationLabel] == "" {
		t.Fatal("candidate generation label missing")
	}
	selector := deployment.Spec.Selector.MatchLabels
	if selector[candidateGenerationLabel] == "" || selector["deployer.io/app"] != cfg.Name {
		t.Fatalf("unexpected candidate selector: %#v", selector)
	}
	if _, stableKey := deployment.Spec.Template.Labels["app.kubernetes.io/name"]; stableKey {
		t.Fatal("candidate pod retained stable service selector label")
	}
	if deployment.Spec.Template.Labels[candidateGenerationLabel] != selector[candidateGenerationLabel] {
		t.Fatal("candidate pod and deployment selectors differ")
	}
	if appconfig.DefaultStateMode != cfg.State.Mode {
		t.Fatal("test fixture is no longer stateless")
	}
}

func TestPrepareCandidateDeploymentIsCreateOnlyAndIdempotent(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cfg := hostingTestConfig(t)
	c := &Controller{namespace: DefaultNamespace, deployments: clientset.AppsV1().Deployments(DefaultNamespace), networkPolicies: clientset.NetworkingV1().NetworkPolicies(DefaultNamespace)}
	requestID := strings.Repeat("b", 64)
	if err := c.PrepareCandidateDeployment(context.Background(), cfg, "", requestID, ""); err != nil {
		t.Fatalf("prepare candidate: %v", err)
	}
	if err := c.PrepareCandidateDeployment(context.Background(), cfg, "", requestID, ""); err != nil {
		t.Fatalf("idempotent prepare: %v", err)
	}
	got, err := c.deployments.Get(context.Background(), candidateDeploymentName(cfg.Name, requestID), metav1.GetOptions{})
	if err != nil || got.Name == "" {
		t.Fatalf("candidate missing: %v", err)
	}
	if len(clientset.Actions()) != 7 { // policy get/create, deployment get/create, then two idempotent gets and final get
		t.Fatalf("candidate preparation unexpectedly mutated existing object: %#v", clientset.Actions())
	}
}

func TestCandidateDeploymentRejectsUnsupportedProfiles(t *testing.T) {
	cfg := testAppConfig()
	if _, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", strings.Repeat("c", 64), ""); err == nil {
		t.Fatal("expected non-hosting profile rejection")
	}
	cfg = hostingTestConfig(t)
	cfg.State.Mode = "stateful"
	if _, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", strings.Repeat("d", 64), ""); err == nil {
		t.Fatal("expected stateful profile rejection")
	}
}

func TestCandidatePolicyConflictPreventsPodCreation(t *testing.T) {
	cfg := hostingTestConfig(t)
	id := strings.Repeat("e", 64)
	policy, err := candidateNetworkPolicyForApp(cfg, DefaultNamespace, id)
	if err != nil {
		t.Fatal(err)
	}
	policy.Labels[appOwnershipLabel] = "another-app"
	client := fake.NewSimpleClientset(policy)
	c := &Controller{namespace: DefaultNamespace, deployments: client.AppsV1().Deployments(DefaultNamespace), networkPolicies: client.NetworkingV1().NetworkPolicies(DefaultNamespace)}
	if err = c.PrepareCandidateDeployment(context.Background(), cfg, "", id, ""); err == nil {
		t.Fatal("foreign network policy accepted")
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "deployments" {
			t.Fatal("candidate accessed before network isolation verified")
		}
	}
}
