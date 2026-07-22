package ingress

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestControllerReconcilesTrafficResilienceResources(t *testing.T) {
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	controller := &Controller{
		namespace:        DefaultNamespace,
		middlewares:      dynamicClient.Resource(retryMiddlewareResource).Namespace(DefaultNamespace),
		serverTransports: dynamicClient.Resource(serversTransportResource).Namespace(DefaultNamespace),
	}
	cfg := testAppConfig()

	if err := controller.reconcileTrafficResilienceResources(context.Background(), cfg); err != nil {
		t.Fatalf("reconcile traffic resilience resources: %v", err)
	}
	middleware, err := controller.middlewares.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get retry middleware: %v", err)
	}
	attempts, found, err := unstructured.NestedInt64(middleware.Object, "spec", "retry", "attempts")
	if err != nil || !found || attempts != 2 {
		t.Fatalf("retry attempts = %d, found=%t, err=%v", attempts, found, err)
	}
	initialInterval, found, err := unstructured.NestedString(
		middleware.Object,
		"spec",
		"retry",
		"initialInterval",
	)
	if err != nil || !found || initialInterval != "100ms" {
		t.Fatalf("retry initial interval = %q, found=%t, err=%v", initialInterval, found, err)
	}
	transport, err := controller.serverTransports.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get servers transport: %v", err)
	}
	dialTimeout, found, err := unstructured.NestedString(
		transport.Object,
		"spec",
		"forwardingTimeouts",
		"dialTimeout",
	)
	if err != nil || !found || dialTimeout != "2s" {
		t.Fatalf("transport dial timeout = %q, found=%t, err=%v", dialTimeout, found, err)
	}

	cfg.Routing.Domain = ""
	if err := controller.reconcileTrafficResilienceResources(context.Background(), cfg); err != nil {
		t.Fatalf("delete traffic resilience resources: %v", err)
	}
	if _, err := controller.middlewares.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("retry middleware was not deleted")
	}
	if _, err := controller.serverTransports.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("servers transport was not deleted")
	}
}
