package ingress

import (
	"context"
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
