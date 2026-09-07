package ingress

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func capacityNode(name string, cpu, memory, ephemeral string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"kubernetes.io/arch": "arm64"}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(cpu),
				corev1.ResourcePods:             resource.MustParse("110"),
				corev1.ResourceMemory:           resource.MustParse(memory),
				corev1.ResourceEphemeralStorage: resource.MustParse(ephemeral),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func capacityConfig(t *testing.T) appconfig.Config {
	cfg := hostingTestConfig(t)
	cfg.Hosting.MaxReplicas = 2
	return cfg
}

func TestPreflightHostingCapacityAccountsForExistingPodsAndSurge(t *testing.T) {
	nodes := fake.NewSimpleClientset(capacityNode("worker-1", "4", "4Gi", "4Gi"))
	pods := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "kube-system"},
		Spec: corev1.PodSpec{NodeName: "worker-1", Containers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("2Gi"), corev1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
		}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})
	controller := &Controller{nodes: nodes.CoreV1().Nodes(), deployments: nodes.AppsV1().Deployments(DefaultNamespace), networkPolicies: nodes.NetworkingV1().NetworkPolicies(DefaultNamespace), capacityPods: pods.CoreV1().Pods("")}
	err := controller.PreflightHosting(context.Background(), capacityConfig(t))
	if err == nil || !strings.Contains(err.Error(), "insufficient eligible Kubernetes capacity") {
		t.Fatalf("expected capacity rejection including existing Pod and surge, got %v", err)
	}
	for _, action := range append(nodes.Actions(), pods.Actions()...) {
		if action.GetVerb() != "list" {
			t.Fatalf("capacity preflight mutated Kubernetes: %v", action)
		}
	}
}

func TestPreflightHostingCapacityRequiresDistinctResilientHosts(t *testing.T) {
	client := fake.NewSimpleClientset(capacityNode("worker-1", "4", "4Gi", "4Gi"), capacityNode("worker-2", "4", "4Gi", "4Gi"))
	controller := &Controller{nodes: client.CoreV1().Nodes(), deployments: client.AppsV1().Deployments(DefaultNamespace), networkPolicies: client.NetworkingV1().NetworkPolicies(DefaultNamespace), capacityPods: client.CoreV1().Pods("")}
	cfg := capacityConfig(t)
	cfg.Resilience.Mode = appconfig.ResilienceResilient
	cfg.Hosting.MaxReplicas = 3
	if err := controller.PreflightHosting(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "requires 3 eligible nodes") {
		t.Fatalf("expected resilient host capacity rejection, got %v", err)
	}
}

func TestPreflightHostingCapacityFailsClosedForUnassignedPod(t *testing.T) {
	nodes := fake.NewSimpleClientset(capacityNode("worker-1", "4", "4Gi", "4Gi"))
	pods := fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "kube-system"}, Status: corev1.PodStatus{Phase: corev1.PodPending}})
	controller := &Controller{nodes: nodes.CoreV1().Nodes(), deployments: nodes.AppsV1().Deployments(DefaultNamespace), networkPolicies: nodes.NetworkingV1().NetworkPolicies(DefaultNamespace), capacityPods: pods.CoreV1().Pods("")}
	if err := controller.PreflightHosting(context.Background(), capacityConfig(t)); err == nil || !strings.Contains(err.Error(), "capacity is unknown") {
		t.Fatalf("expected unknown pending placement rejection, got %v", err)
	}
}
