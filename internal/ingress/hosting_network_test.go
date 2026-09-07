package ingress

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNetworkPolicyForHostedAppIsScopedAndExplicit(t *testing.T) {
	cfg := hostingTestConfig(t)
	cfg.Metrics = &appconfig.MetricsConfig{Port: 9090, Path: "/metrics"}
	cfg.Hosting.Network.Egress = []appconfig.HostingEgressRule{{CIDR: "203.0.113.0/24", Ports: []int{443, 5432}}}
	policy, err := networkPolicyForHostedApp(cfg, DefaultNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Spec.PodSelector.MatchLabels[hostingProfileLabel] != "v1" || policy.Spec.PodSelector.MatchLabels[appOwnershipLabel] != cfg.Name {
		t.Fatalf("policy selects more than the opted-in app: %#v", policy.Spec.PodSelector)
	}
	if policy.Labels[hostingProfileLabel] != "v1" || policy.Labels[appOwnershipLabel] != cfg.Name {
		t.Fatalf("missing ownership/profile labels: %#v", policy.Labels)
	}
	if len(policy.Spec.Ingress) != 2 || len(policy.Spec.Egress) != 2 {
		t.Fatalf("unexpected policy rules: ingress=%d egress=%d", len(policy.Spec.Ingress), len(policy.Spec.Egress))
	}
	if got := policy.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/name"]; got != "traefik" {
		t.Fatalf("unexpected Traefik selector: %q", got)
	}
	if got := policy.Spec.Ingress[1].Ports; len(got) != 2 || got[0].Port.IntValue() != 8080 || got[1].Port.IntValue() != 9090 {
		t.Fatalf("monitoring was not limited to service/metrics ports: %#v", got)
	}
	if got := policy.Spec.Egress[1].To[0].IPBlock.CIDR; got != "203.0.113.0/24" {
		t.Fatalf("unexpected explicit egress CIDR: %q", got)
	}
}

func TestNetworkPolicyOwnershipCollisionAndDelete(t *testing.T) {
	cfg := hostingTestConfig(t)
	owned := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: DefaultNamespace, Labels: map[string]string{appOwnershipLabel: cfg.Name, managedByLabel: managedByDeployer, hostingProfileLabel: "v1"}}}
	client := fake.NewSimpleClientset(owned)
	controller := &Controller{namespace: DefaultNamespace, pods: client.CoreV1().Pods(DefaultNamespace), networkPolicies: client.NetworkingV1().NetworkPolicies(DefaultNamespace)}
	if err := controller.reconcileHostingNetworkPolicy(context.Background(), cfg); err != nil {
		t.Fatalf("reconcile owned policy: %v", err)
	}
	if err := controller.deleteHostingNetworkPolicy(context.Background(), cfg.Name); err != nil {
		t.Fatalf("delete owned policy: %v", err)
	}
	if _, err := client.NetworkingV1().NetworkPolicies(DefaultNamespace).Get(context.Background(), cfg.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("owned hosting policy was not deleted")
	}

	foreign := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: DefaultNamespace, Labels: map[string]string{appOwnershipLabel: "other-app", hostingProfileLabel: "v1"}}}
	foreignClient := fake.NewSimpleClientset(foreign)
	foreignController := &Controller{namespace: DefaultNamespace, networkPolicies: foreignClient.NetworkingV1().NetworkPolicies(DefaultNamespace)}
	if err := foreignController.reconcileHostingNetworkPolicy(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "ownership conflict") {
		t.Fatalf("expected ownership collision, got %v", err)
	}
}

func TestLegacyConfigDoesNotRenderNetworkPolicy(t *testing.T) {
	cfg := testAppConfig()
	policy, err := networkPolicyForHostedApp(cfg, DefaultNamespace)
	if err != nil || policy != nil {
		t.Fatalf("legacy app unexpectedly received NetworkPolicy: policy=%#v err=%v", policy, err)
	}
}

func TestHostedPolicyRetainedUntilPodsDisappear(t *testing.T) {
	cfg := hostingTestConfig(t)
	policy, err := networkPolicyForHostedApp(cfg, DefaultNamespace)
	if err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "terminating", Namespace: DefaultNamespace, Labels: map[string]string{"deployer.io/app": cfg.Name, hostingProfileLabel: "v1"}}}
	client := fake.NewSimpleClientset(policy, pod)
	controller := &Controller{namespace: DefaultNamespace, networkPolicies: client.NetworkingV1().NetworkPolicies(DefaultNamespace), pods: client.CoreV1().Pods(DefaultNamespace)}
	if err := controller.deleteHostingNetworkPolicy(context.Background(), cfg.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.networkPolicies.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err != nil {
		t.Fatal("policy removed while workload still exists")
	}
	if err := controller.pods.Delete(context.Background(), pod.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := controller.deleteHostingNetworkPolicy(context.Background(), cfg.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.networkPolicies.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("policy retained after cleanup became safe")
	}
}
