package ingress

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCandidateBootstrapSelectsNoCandidateAndNeverOverwrites(t *testing.T) {
	cfg := hostingTestConfig(t)
	id := strings.Repeat("a", 64)
	client := fake.NewSimpleClientset()
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace)}
	if err := c.CreateInactiveCandidateService(context.Background(), cfg, id); err != nil {
		t.Fatal(err)
	}
	before, err := c.services.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !IsInactiveCandidateTarget(cfg, id, ActivationTarget{Selector: before.Spec.Selector, Ports: before.Spec.Ports}) {
		t.Fatal("bootstrap target is not recognized as inactive")
	}
	for _, request := range []string{id, strings.Repeat("b", 64)} {
		candidate, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", request, "")
		if err != nil {
			t.Fatal(err)
		}
		if labels.SelectorFromSet(before.Spec.Selector).Matches(labels.Set(candidate.Spec.Template.Labels)) {
			t.Fatal("bootstrap routes to candidate before activation")
		}
	}
	if err = c.CreateInactiveCandidateService(context.Background(), cfg, strings.Repeat("b", 64)); err == nil {
		t.Fatal("existing service silently replaced")
	}
	after, err := c.services.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("bootstrap changed existing service")
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "delete" {
			t.Fatal("bootstrap mutated existing routing")
		}
	}
}

func TestResetInactiveCandidateServiceUsesCASAndPreservesUID(t *testing.T) {
	cfg := hostingTestConfig(t)
	oldID := strings.Repeat("a", 64)
	newID := strings.Repeat("b", 64)
	client := fake.NewSimpleClientset()
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace)}
	if err := c.CreateInactiveCandidateService(context.Background(), cfg, oldID); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := c.services.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap.UID = "service-1"
	bootstrap.ResourceVersion = "1"
	bootstrap.Annotations[activationOperationAnnotation] = strings.Repeat("c", 64)
	if _, err = c.services.Update(context.Background(), bootstrap, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	gate, target, err := c.CaptureActivationGate(context.Background(), cfg.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.InitialCandidateRequest(context.Background(), gate); err != nil || got != oldID {
		t.Fatalf("initial request: %q %v", got, err)
	}
	updatedGate, updatedTarget, err := c.ResetInactiveCandidateService(context.Background(), gate, cfg, oldID, newID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedGate.UID != gate.UID {
		t.Fatalf("service identity was not CAS preserved: before=%+v after=%+v", gate, updatedGate)
	}
	if IsInactiveCandidateTarget(cfg, oldID, updatedTarget) || !IsInactiveCandidateTarget(cfg, newID, updatedTarget) {
		t.Fatalf("unexpected reset target: %+v", updatedTarget)
	}
	if target.Selector[appOwnershipLabel] != updatedTarget.Selector[appOwnershipLabel] {
		t.Fatal("reset changed service ownership")
	}
	if got, err := c.InitialCandidateRequest(context.Background(), updatedGate); err != nil || got != newID {
		t.Fatalf("updated request: %q %v", got, err)
	}
	if _, err := c.FenceActivationGate(context.Background(), gate, strings.Repeat("d", 64)); !errors.Is(err, ErrActivationSuperseded) {
		t.Fatalf("old recovery writer reacquired reset service: %v", err)
	}
	if _, _, err := c.ResetInactiveCandidateService(context.Background(), gate, cfg, oldID, strings.Repeat("c", 64)); err == nil {
		t.Fatal("stale activation gate was accepted")
	}
}
