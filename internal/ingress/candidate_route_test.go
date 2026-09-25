package ingress

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func candidateRouteController(t *testing.T, cfg appconfig.Config, operation string) (*Controller, ActivationGate, ActivationTarget, *fake.Clientset) {
	t.Helper()
	clientset := fake.NewSimpleClientset()
	service := candidateServiceForApp(cfg, DefaultNamespace)
	service.UID = types.UID("service-uid")
	service.ResourceVersion = "7"
	if service.Annotations == nil {
		service.Annotations = map[string]string{}
	}
	service.Annotations[activationOperationAnnotation] = operation
	if _, err := clientset.CoreV1().Services(DefaultNamespace).Create(context.Background(), service, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create service: %v", err)
	}
	c := &Controller{namespace: DefaultNamespace, tls: TLSConfig{}.WithDefaults(), services: clientset.CoreV1().Services(DefaultNamespace), ingresses: clientset.NetworkingV1().Ingresses(DefaultNamespace)}
	gate := ActivationGate{App: cfg.Name, Namespace: DefaultNamespace, UID: service.UID, ResourceVersion: service.ResourceVersion, OperationID: operation}
	target := ActivationTarget{Selector: service.Spec.Selector, Ports: service.Spec.Ports}
	return c, gate, target, clientset
}

func TestReconcileCandidateRouteVerifiesGateBeforeIngressWrite(t *testing.T) {
	cfg := hostingTestConfig(t)
	cfg.Routing.Domain = "api.example.test"
	id := strings.Repeat("a", 64)
	c, gate, target, _ := candidateRouteController(t, cfg, id)
	if err := c.ReconcileCandidateRoute(context.Background(), gate, target, cfg); err != nil {
		t.Fatalf("reconcile route: %v", err)
	}
	if _, err := c.ingresses.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected Ingress: %v", err)
	}
	wrong := target
	wrong.Selector = map[string]string{"deployer.io/app": cfg.Name}
	if err := c.ReconcileCandidateRoute(context.Background(), gate, wrong, cfg); err == nil {
		t.Fatal("mismatched target accepted")
	}
}

func TestReconcileCandidateRouteDeletesOnlyOwnedIngressWhenDomainEmpty(t *testing.T) {
	cfg := hostingTestConfig(t)
	id := strings.Repeat("b", 64)
	c, gate, target, _ := candidateRouteController(t, cfg, id)
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: DefaultNamespace, UID: types.UID("ingress-uid"), ResourceVersion: "4", Labels: managedAppLabels(cfg.Name)}}
	if _, err := c.ingresses.Create(context.Background(), ingress, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ingress: %v", err)
	}
	if err := c.ReconcileCandidateRoute(context.Background(), gate, target, cfg); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if _, err := c.ingresses.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("Ingress remained after empty domain route")
	}
}

func TestReconcileCandidateRouteUpdatesOwnedIngressWithCASIdentity(t *testing.T) {
	cfg := hostingTestConfig(t)
	cfg.Routing.Domain = "api.example.test"
	id := strings.Repeat("c", 64)
	c, gate, target, _ := candidateRouteController(t, cfg, id)
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: DefaultNamespace, UID: types.UID("ingress-uid"), ResourceVersion: "4", Labels: managedAppLabels(cfg.Name)}}
	if _, err := c.ingresses.Create(context.Background(), ingress, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ingress: %v", err)
	}
	if err := c.ReconcileCandidateRoute(context.Background(), gate, target, cfg); err != nil {
		t.Fatalf("update route: %v", err)
	}
	updated, err := c.ingresses.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil || updated.Spec.Rules[0].Host != cfg.Routing.Domain {
		t.Fatalf("updated ingress: %#v err=%v", updated, err)
	}
	if updated.UID != ingress.UID {
		t.Fatalf("update dropped UID: %q", updated.UID)
	}
}

func TestReconcileCandidateRouteRefusesForeignIngressAndStaleOrDeletingService(t *testing.T) {
	cfg := hostingTestConfig(t)
	cfg.Routing.Domain = "api.example.test"
	id := strings.Repeat("d", 64)
	c, gate, target, _ := candidateRouteController(t, cfg, id)
	foreign := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: DefaultNamespace, UID: types.UID("foreign"), ResourceVersion: "4", Labels: managedAppLabels("other")}}
	if _, err := c.ingresses.Create(context.Background(), foreign, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create foreign ingress: %v", err)
	}
	if err := c.ReconcileCandidateRoute(context.Background(), gate, target, cfg); err == nil {
		t.Fatal("foreign ingress was updated")
	}
	stale := gate
	stale.ResourceVersion = "stale"
	if err := c.ReconcileCandidateRoute(context.Background(), stale, target, cfg); err == nil {
		t.Fatal("stale gate was accepted")
	}
	service, _ := c.services.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	when := metav1.Now()
	service.DeletionTimestamp = &when
	if _, err := c.services.Update(context.Background(), service, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("mark service deleting: %v", err)
	}
	if err := c.ReconcileCandidateRoute(context.Background(), gate, target, cfg); err == nil {
		t.Fatal("deleting service was accepted")
	}
}

func TestReconcileCandidateRouteDeleteUsesUIDAndResourceVersion(t *testing.T) {
	cfg := hostingTestConfig(t)
	id := strings.Repeat("e", 64)
	c, gate, target, clientset := candidateRouteController(t, cfg, id)
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: DefaultNamespace, UID: types.UID("ingress-uid"), ResourceVersion: "4", Labels: managedAppLabels(cfg.Name)}}
	if _, err := c.ingresses.Create(context.Background(), ingress, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ingress: %v", err)
	}
	if err := c.ReconcileCandidateRoute(context.Background(), gate, target, cfg); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "ingresses" {
			deleteAction := action.(interface{ GetDeleteOptions() metav1.DeleteOptions })
			options := deleteAction.GetDeleteOptions()
			if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != ingress.UID || options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion != ingress.ResourceVersion {
				t.Fatalf("delete lacked UID/resourceVersion preconditions: %#v", options.Preconditions)
			}
			return
		}
	}
	t.Fatal("delete action not recorded")
}

func TestCandidateRouteCreatesReferencedProxyDependencies(t *testing.T) {
	cfg := hostingTestConfig(t)
	cfg.Routing.Domain = "api.example.test"
	c, gate, target, _ := candidateRouteController(t, cfg, strings.Repeat("f", 64))
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	c.middlewares = dynamicClient.Resource(retryMiddlewareResource).Namespace(DefaultNamespace)
	c.serverTransports = dynamicClient.Resource(serversTransportResource).Namespace(DefaultNamespace)
	if err := c.ReconcileCandidateRoute(context.Background(), gate, target, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := c.middlewares.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err != nil {
		t.Fatal("referenced middleware missing:", err)
	}
	if _, err := c.serverTransports.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err != nil {
		t.Fatal("referenced transport missing:", err)
	}
}
