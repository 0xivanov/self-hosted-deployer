package ingress

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRetireCandidateCreatesPersistentZeroReplicaTombstone(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cfg := hostingTestConfig(t)
	c := &Controller{namespace: DefaultNamespace, deployments: clientset.AppsV1().Deployments(DefaultNamespace), pods: clientset.CoreV1().Pods(DefaultNamespace), networkPolicies: clientset.NetworkingV1().NetworkPolicies(DefaultNamespace)}
	id := strings.Repeat("a", 64)
	if err := c.RetireCandidateDeployment(context.Background(), cfg, "", id, ""); err != nil {
		t.Fatalf("retire missing candidate: %v", err)
	}
	name := candidateDeploymentName(cfg.Name, id)
	got, err := c.deployments.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get tombstone: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 || got.Annotations[candidateRetiredAnnotation] != "true" {
		t.Fatalf("unexpected tombstone: %#v", got)
	}
	got.UID = "candidate-uid"
	got.ResourceVersion = "1"
	if _, err := c.deployments.Update(context.Background(), got, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("set tombstone identity: %v", err)
	}
	if err := c.RetireCandidateDeployment(context.Background(), cfg, "", id, ""); err != nil {
		t.Fatalf("idempotent retire: %v", err)
	}
}

func TestRetireCandidateCASUpdatesExistingAndProofRequiresObservedDrain(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cfg := hostingTestConfig(t)
	c := &Controller{namespace: DefaultNamespace, deployments: clientset.AppsV1().Deployments(DefaultNamespace), pods: clientset.CoreV1().Pods(DefaultNamespace), networkPolicies: clientset.NetworkingV1().NetworkPolicies(DefaultNamespace)}
	id := strings.Repeat("b", 64)
	desired, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", id, "")
	if err != nil {
		t.Fatalf("render candidate: %v", err)
	}
	desired.UID = "candidate-uid"
	desired.ResourceVersion = "1"
	created, err := c.deployments.Create(context.Background(), desired, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	if err := c.RetireCandidateDeployment(context.Background(), cfg, "", id, ""); err != nil {
		t.Fatalf("retire existing: %v", err)
	}
	got, err := c.deployments.Get(context.Background(), created.Name, metav1.GetOptions{})
	if err != nil || got.Spec.Replicas == nil || *got.Spec.Replicas != 0 || got.Annotations[candidateRetiredAnnotation] != "true" {
		t.Fatalf("retired candidate: %#v err=%v", got, err)
	}
	ready, err := c.CandidateRetired(context.Background(), cfg, "", id, "")
	if err != nil || ready {
		t.Fatalf("proof accepted before observed drain: ready=%v err=%v", ready, err)
	}
	got.Generation = 1
	if _, err := c.deployments.Update(context.Background(), got, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("set generation: %v", err)
	}
	got, _ = c.deployments.Get(context.Background(), created.Name, metav1.GetOptions{})
	got.Status.ObservedGeneration = got.Generation
	if _, err := c.deployments.UpdateStatus(context.Background(), got, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("set observed generation: %v", err)
	}
	selector, _ := CandidateSelector(cfg.Name, id)
	if _, err := c.pods.Create(context.Background(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "candidate-pod", Namespace: DefaultNamespace, Labels: selector}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create candidate pod: %v", err)
	}
	ready, err = c.CandidateRetired(context.Background(), cfg, "", id, "")
	if err != nil || ready {
		t.Fatalf("proof accepted with live candidate pod: ready=%v err=%v", ready, err)
	}
	if err := c.pods.Delete(context.Background(), "candidate-pod", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete candidate pod: %v", err)
	}
	ready, err = c.CandidateRetired(context.Background(), cfg, "", id, "")
	if err != nil || !ready {
		t.Fatalf("expected retired proof: ready=%v err=%v", ready, err)
	}
}

func TestRetireCandidateRejectsForeignOrMutatedDeployment(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cfg := hostingTestConfig(t)
	c := &Controller{namespace: DefaultNamespace, deployments: clientset.AppsV1().Deployments(DefaultNamespace), pods: clientset.CoreV1().Pods(DefaultNamespace), networkPolicies: clientset.NetworkingV1().NetworkPolicies(DefaultNamespace)}
	id := strings.Repeat("c", 64)
	desired, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", id, "")
	if err != nil {
		t.Fatalf("render candidate: %v", err)
	}
	desired.Labels[appOwnershipLabel] = "other-app"
	if _, err := c.deployments.Create(context.Background(), desired, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create foreign candidate: %v", err)
	}
	if err := c.RetireCandidateDeployment(context.Background(), cfg, "", id, ""); err == nil {
		t.Fatal("foreign candidate was retired")
	}
}

func TestRetireCandidateBlocksLateCreateAndPreparation(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cfg := hostingTestConfig(t)
	id := strings.Repeat("d", 64)
	c := &Controller{namespace: DefaultNamespace, deployments: clientset.AppsV1().Deployments(DefaultNamespace), pods: clientset.CoreV1().Pods(DefaultNamespace), networkPolicies: clientset.NetworkingV1().NetworkPolicies(DefaultNamespace)}
	desired, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", id, "")
	if err != nil {
		t.Fatalf("render candidate: %v", err)
	}
	if err := c.RetireCandidateDeployment(context.Background(), cfg, "", id, ""); err != nil {
		t.Fatalf("retire missing candidate: %v", err)
	}
	if _, err := c.deployments.Create(context.Background(), desired, metav1.CreateOptions{}); err == nil {
		t.Fatal("late candidate create unexpectedly succeeded")
	}
	if err := c.PrepareCandidateDeployment(context.Background(), cfg, "", id, ""); err == nil {
		t.Fatal("prepare resurrected retired candidate")
	}
	if pods, err := c.pods.List(context.Background(), metav1.ListOptions{}); err != nil || len(pods.Items) != 0 {
		t.Fatalf("retirement created pods: %d, %v", len(pods.Items), err)
	}
}
