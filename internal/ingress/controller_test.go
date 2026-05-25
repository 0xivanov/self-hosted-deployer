package ingress

import (
	"context"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestControllerReconcilesAndDeletesAppResources(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	controller := &Controller{
		namespace:   DefaultNamespace,
		tls:         TLSConfig{}.WithDefaults(),
		namespaces:  clientset.CoreV1().Namespaces(),
		ingresses:   clientset.NetworkingV1().Ingresses(DefaultNamespace),
		services:    clientset.CoreV1().Services(DefaultNamespace),
		appSecrets:  clientset.CoreV1().Secrets(DefaultNamespace),
		deployments: clientset.AppsV1().Deployments(DefaultNamespace),
	}
	cfg := testAppConfig()

	if err := controller.Reconcile(context.Background(), cfg, nil, ""); err != nil {
		t.Fatalf("create app resources: %v", err)
	}
	serviceCreate, ingressCreate := -1, -1
	for i, action := range clientset.Actions() {
		if action.GetVerb() != "create" {
			continue
		}
		switch action.GetResource().Resource {
		case "services":
			serviceCreate = i
		case "ingresses":
			ingressCreate = i
		}
	}
	if serviceCreate < 0 || ingressCreate < 0 || serviceCreate > ingressCreate {
		t.Fatalf("expected service creation before ingress creation, got actions %#v", clientset.Actions())
	}
	if _, err := controller.namespaces.Get(context.Background(), DefaultNamespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("get managed namespace: %v", err)
	}
	service, err := controller.services.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil || service.Spec.Ports[0].Port != 3000 {
		t.Fatalf("unexpected service %#v: %v", service, err)
	}
	deployment, err := controller.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != "ivan/my-api:1.0.0" ||
		deployment.Spec.Template.Spec.NodeSelector["kubernetes.io/arch"] != "arm64" ||
		len(deployment.Spec.Template.Spec.TopologySpreadConstraints) != 1 {
		t.Fatalf("unexpected deployment: %#v", deployment.Spec.Template.Spec)
	}

	cfg.Routing.Domain = "new.example.com"
	cfg.Image = "ivan/my-api:1.0.1"
	if err := controller.Reconcile(context.Background(), cfg, nil, ""); err != nil {
		t.Fatalf("update app resources: %v", err)
	}
	got, err := controller.ingresses.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated ingress: %v", err)
	}
	if got.Spec.Rules[0].Host != "new.example.com" {
		t.Fatalf("unexpected updated host: %#v", got.Spec.Rules)
	}
	deployment, err = controller.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil || deployment.Spec.Template.Spec.Containers[0].Image != "ivan/my-api:1.0.1" {
		t.Fatalf("expected updated deployment image, got %#v: %v", deployment, err)
	}

	cfg.Routing.Domain = ""
	if err := controller.Reconcile(context.Background(), cfg, nil, ""); err != nil {
		t.Fatalf("delete ingress: %v", err)
	}
	if _, err := controller.ingresses.Get(context.Background(), cfg.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected deleted ingress, got %v", err)
	}
	if _, err := controller.services.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected an app without a public route to retain its service: %v", err)
	}

	if err := controller.Delete(context.Background(), cfg.Name); err != nil {
		t.Fatalf("delete app resources: %v", err)
	}
	if _, err := controller.services.Get(context.Background(), cfg.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected deleted service, got %v", err)
	}
	if _, err := controller.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected deleted deployment, got %v", err)
	}
}

func TestControllerCreatesIssuerAndTLSIngress(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	controller := &Controller{
		namespace:   DefaultNamespace,
		tls:         TLSConfig{ACMEEmail: "ops@example.com"}.WithDefaults(),
		namespaces:  clientset.CoreV1().Namespaces(),
		ingresses:   clientset.NetworkingV1().Ingresses(DefaultNamespace),
		services:    clientset.CoreV1().Services(DefaultNamespace),
		appSecrets:  clientset.CoreV1().Secrets(DefaultNamespace),
		deployments: clientset.AppsV1().Deployments(DefaultNamespace),
		issuers:     dynamicClient.Resource(clusterIssuerResource),
	}
	cfg := testAppConfig()

	if err := controller.Reconcile(context.Background(), cfg, nil, ""); err != nil {
		t.Fatalf("create TLS ingress: %v", err)
	}
	issuer, err := controller.issuers.Get(context.Background(), DefaultClusterIssuer, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get issuer: %v", err)
	}
	email, _, err := unstructured.NestedString(issuer.Object, "spec", "acme", "email")
	if err != nil || email != "ops@example.com" {
		t.Fatalf("unexpected issuer email %q: %v", email, err)
	}
	got, err := controller.ingresses.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get TLS ingress: %v", err)
	}
	if len(got.Spec.TLS) != 1 || got.Spec.TLS[0].SecretName != "my-api-tls" {
		t.Fatalf("unexpected ingress TLS: %#v", got.Spec.TLS)
	}
}

func TestControllerAppliesReferencedSecretsBeforeDeploymentAndRestartsOnChange(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	controller := &Controller{
		namespace:   DefaultNamespace,
		tls:         TLSConfig{}.WithDefaults(),
		namespaces:  clientset.CoreV1().Namespaces(),
		ingresses:   clientset.NetworkingV1().Ingresses(DefaultNamespace),
		services:    clientset.CoreV1().Services(DefaultNamespace),
		appSecrets:  clientset.CoreV1().Secrets(DefaultNamespace),
		deployments: clientset.AppsV1().Deployments(DefaultNamespace),
	}
	cfg := testAppConfig()
	cfg.Secrets = []string{"DATABASE_URL"}

	if err := controller.Reconcile(context.Background(), cfg, map[string]string{"DATABASE_URL": "postgres://first"}, "encrypted-revision-1"); err != nil {
		t.Fatalf("create app secret resources: %v", err)
	}
	secretCreate, deploymentCreate := -1, -1
	for i, action := range clientset.Actions() {
		if action.GetVerb() != "create" {
			continue
		}
		switch action.GetResource().Resource {
		case "secrets":
			secretCreate = i
		case "deployments":
			deploymentCreate = i
		}
	}
	if secretCreate < 0 || deploymentCreate < 0 || secretCreate > deploymentCreate {
		t.Fatalf("expected Secret creation before Deployment creation, got actions %#v", clientset.Actions())
	}
	secret, err := controller.appSecrets.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil || string(secret.Data["DATABASE_URL"]) != "postgres://first" {
		t.Fatalf("unexpected Kubernetes Secret %#v: %v", secret, err)
	}
	deployment, err := controller.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	env := deployment.Spec.Template.Spec.Containers[0].Env
	if len(env) != 1 || env[0].Name != "DATABASE_URL" || env[0].ValueFrom.SecretKeyRef.Key != "DATABASE_URL" {
		t.Fatalf("unexpected deployment secret environment: %#v", env)
	}
	originalHash := deployment.Spec.Template.Annotations[secretHashAnnotation]

	if err := controller.Reconcile(context.Background(), cfg, map[string]string{"DATABASE_URL": "postgres://updated"}, "encrypted-revision-2"); err != nil {
		t.Fatalf("update app secret resources: %v", err)
	}
	deployment, err = controller.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil || deployment.Spec.Template.Annotations[secretHashAnnotation] == originalHash {
		t.Fatalf("expected changed secret hash annotation, got %#v: %v", deployment, err)
	}
}

func TestControllerStatusUsesAvailableReplicas(t *testing.T) {
	tests := []struct {
		name      string
		desired   int32
		available int32
		want      string
	}{
		{name: "healthy", desired: 2, available: 2, want: StatusHealthy},
		{name: "degraded", desired: 2, available: 1, want: StatusDegraded},
		{name: "unavailable", desired: 2, available: 0, want: StatusUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "my-api", Namespace: DefaultNamespace},
				Spec:       appsv1.DeploymentSpec{Replicas: &tt.desired},
				Status:     appsv1.DeploymentStatus{AvailableReplicas: tt.available},
			})
			controller := &Controller{
				namespace:   DefaultNamespace,
				ingresses:   clientset.NetworkingV1().Ingresses(DefaultNamespace),
				services:    clientset.CoreV1().Services(DefaultNamespace),
				deployments: clientset.AppsV1().Deployments(DefaultNamespace),
			}
			got, err := controller.Status(context.Background(), "my-api")
			if err != nil {
				t.Fatalf("read route status: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got status %q, want %q", got, tt.want)
			}
		})
	}
}

func testAppConfig() appconfig.Config {
	return appconfig.Config{
		Name:  "my-api",
		Image: "ivan/my-api:1.0.0",
		Service: appconfig.ServiceConfig{
			Port:   3000,
			Health: appconfig.HealthConfig{Path: "/health"},
		},
		Routing:   appconfig.RoutingConfig{Domain: "api.example.com"},
		Deploy:    appconfig.DeployConfig{Replicas: 2},
		Placement: appconfig.PlacementConfig{Arch: "linux/arm64", Spread: true},
		State:     appconfig.StateConfig{Mode: "stateless"},
	}
}
