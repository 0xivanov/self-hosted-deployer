package ingress

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestActivationGateRejectsLateWriterAfterRecovery(t *testing.T) {
	ctx := context.Background()
	stored := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "site", Namespace: DefaultNamespace, UID: "service-1", ResourceVersion: "1", Labels: managedAppLabels("site")}, Spec: corev1.ServiceSpec{Selector: map[string]string{"release": "old"}, Ports: []corev1.ServicePort{{Port: 80}}, ClusterIP: "10.0.0.10"}}
	client := fake.NewSimpleClientset()
	// The standard fake does not enforce optimistic concurrency. This reactor
	// models the API server's atomic resourceVersion comparison explicitly.
	client.PrependReactor("get", "services", func(ktesting.Action) (bool, runtime.Object, error) { return true, stored.DeepCopy(), nil })
	writes := 0
	var beforeUpdate func()
	client.PrependReactor("update", "services", func(a ktesting.Action) (bool, runtime.Object, error) {
		candidate := a.(ktesting.UpdateAction).GetObject().(*corev1.Service)
		if beforeUpdate != nil {
			f := beforeUpdate
			beforeUpdate = nil
			f()
		}
		if candidate.ResourceVersion != stored.ResourceVersion || candidate.UID != stored.UID {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "services"}, "site", errors.New("stale resource version"))
		}
		writes++
		next, _ := strconv.Atoi(stored.ResourceVersion)
		stored = candidate.DeepCopy()
		stored.ResourceVersion = strconv.Itoa(next + 1)
		return true, stored.DeepCopy(), nil
	})
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace)}
	captured, previous, err := c.CaptureActivationGate(ctx, "site")
	if err != nil {
		t.Fatal(err)
	}
	candidateGate, err := c.FenceActivationGate(ctx, captured, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	target := ActivationTarget{Selector: map[string]string{"release": "candidate"}, Ports: []corev1.ServicePort{{Port: 3000}}}
	// Pause the old operation after its GET. Recovery wins the UPDATE race.
	beforeUpdate = func() {
		// Simulate another API client completing the recovery while this
		// request is in flight. Nested fake-client calls would deadlock the
		// fake's reactor mutex, unlike independent real API clients.
		next, _ := strconv.Atoi(stored.ResourceVersion)
		stored.ResourceVersion = strconv.Itoa(next + 1)
		stored.Annotations[activationOperationAnnotation] = strings.Repeat("b", 64)
		stored.Spec.Selector = previous.Selector
		stored.Spec.Ports = previous.Ports
	}
	if _, err = c.ReplaceActivationTarget(ctx, candidateGate, target); !errors.Is(err, ErrActivationSuperseded) {
		t.Fatalf("late activation accepted: %v", err)
	}
	if stored.Spec.Selector["release"] != "old" || stored.Spec.Ports[0].Port != 80 || stored.Spec.ClusterIP != "10.0.0.10" {
		t.Fatalf("recovery selection changed: %+v", stored.Spec)
	}
	before := writes
	// Reconstructed controller, same persisted token, cannot retry the old write.
	restarted := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace)}
	if _, err = restarted.ReplaceActivationTarget(ctx, candidateGate, target); !errors.Is(err, ErrActivationSuperseded) || writes != before {
		t.Fatalf("stale replay after restart: %v", err)
	}
}

func TestActivationGateRejectsRecreatedOrForeignService(t *testing.T) {
	ctx := context.Background()
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "site", Namespace: DefaultNamespace, UID: "original", ResourceVersion: "1", Labels: managedAppLabels("site")}}
	client := fake.NewSimpleClientset(service)
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace)}
	gate, _, err := c.CaptureActivationGate(ctx, "site")
	if err != nil {
		t.Fatal(err)
	}
	service.UID = "replacement"
	if _, err = c.services.Update(ctx, service, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.FenceActivationGate(ctx, gate, strings.Repeat("a", 64)); !errors.Is(err, ErrActivationSuperseded) {
		t.Fatalf("recreated service accepted: %v", err)
	}
	service.Labels = managedAppLabels("another-site")
	if _, err = c.services.Update(ctx, service, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.CaptureActivationGate(ctx, "site"); err == nil {
		t.Fatal("foreign service accepted")
	}
}
