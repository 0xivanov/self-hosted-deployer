package ingress

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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

func TestControllerOrdersRouteDependenciesWithoutBreakingTraffic(t *testing.T) {
	clientset := fake.NewSimpleClientset(testReadyWorker())
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	var routingActions []string
	dynamicClient.PrependReactor("*", "*", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		if action.GetVerb() != "create" && action.GetVerb() != "update" && action.GetVerb() != "delete" {
			return false, nil, nil
		}
		switch action.GetResource().Resource {
		case retryMiddlewareResource.Resource, serversTransportResource.Resource:
			routingActions = append(routingActions, action.GetResource().Resource)
		}
		return false, nil, nil
	})
	clientset.PrependReactor("*", "*", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		if action.GetVerb() == "create" || action.GetVerb() == "update" || action.GetVerb() == "delete" {
			switch action.GetResource().Resource {
			case "services", "ingresses":
				routingActions = append(routingActions, action.GetResource().Resource)
			}
		}
		return false, nil, nil
	})
	controller := &Controller{
		namespace:        DefaultNamespace,
		tls:              TLSConfig{}.WithDefaults(),
		namespaces:       clientset.CoreV1().Namespaces(),
		ingresses:        clientset.NetworkingV1().Ingresses(DefaultNamespace),
		services:         clientset.CoreV1().Services(DefaultNamespace),
		appSecrets:       clientset.CoreV1().Secrets(DefaultNamespace),
		nodes:            clientset.CoreV1().Nodes(),
		pdbs:             clientset.PolicyV1().PodDisruptionBudgets(DefaultNamespace),
		deployments:      clientset.AppsV1().Deployments(DefaultNamespace),
		middlewares:      dynamicClient.Resource(retryMiddlewareResource).Namespace(DefaultNamespace),
		serverTransports: dynamicClient.Resource(serversTransportResource).Namespace(DefaultNamespace),
	}
	cfg := testAppConfig()

	if err := controller.Reconcile(context.Background(), cfg, nil, ""); err != nil {
		t.Fatalf("create routed app: %v", err)
	}
	wantPublic := []string{
		retryMiddlewareResource.Resource,
		serversTransportResource.Resource,
		"services",
		"ingresses",
	}
	if !reflect.DeepEqual(routingActions, wantPublic) {
		t.Fatalf("public route actions = %v, want %v", routingActions, wantPublic)
	}

	routingActions = nil
	cfg.Routing.Domain = ""
	if err := controller.Reconcile(context.Background(), cfg, nil, ""); err != nil {
		t.Fatalf("remove public route: %v", err)
	}
	wantPrivate := []string{
		"ingresses",
		"services",
		retryMiddlewareResource.Resource,
		serversTransportResource.Resource,
	}
	if !reflect.DeepEqual(routingActions, wantPrivate) {
		t.Fatalf("private route actions = %v, want %v", routingActions, wantPrivate)
	}
}
