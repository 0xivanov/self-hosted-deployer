package ingress

import (
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
)

func TestManifestForAppRoutesDomainToService(t *testing.T) {
	manifest, ok, err := ManifestForApp(appconfig.Config{
		Name: "my-api",
		Service: appconfig.ServiceConfig{
			Port: 3000,
		},
		Routing: appconfig.RoutingConfig{
			Domain: "api.example.com",
		},
	}, "", TLSConfig{})
	if err != nil {
		t.Fatalf("generate ingress: %v", err)
	}
	if !ok {
		t.Fatal("expected ingress manifest")
	}
	if manifest.APIVersion != "networking.k8s.io/v1" || manifest.Kind != "Ingress" {
		t.Fatalf("unexpected manifest type: %#v", manifest)
	}
	if manifest.Metadata.Name != "my-api" || manifest.Metadata.Namespace != DefaultNamespace {
		t.Fatalf("unexpected metadata: %#v", manifest.Metadata)
	}
	if manifest.Metadata.Annotations[traefikRetryMiddlewareAnnotation] !=
		traefikResourceReference(DefaultNamespace, "my-api") {
		t.Fatalf("unexpected retry middleware annotation: %#v", manifest.Metadata.Annotations)
	}
	if len(manifest.Spec.Rules) != 1 || manifest.Spec.Rules[0].Host != "api.example.com" {
		t.Fatalf("unexpected rules: %#v", manifest.Spec.Rules)
	}
	path := manifest.Spec.Rules[0].HTTP.Paths[0]
	if path.Path != "/" || path.PathType != "Prefix" {
		t.Fatalf("unexpected path: %#v", path)
	}
	if path.Backend.Service.Name != "my-api" || path.Backend.Service.Port.Number != 3000 {
		t.Fatalf("unexpected backend: %#v", path.Backend)
	}
}

func TestManifestForAppSkipsEmptyDomain(t *testing.T) {
	_, ok, err := ManifestForApp(appconfig.Config{
		Name:    "my-api",
		Service: appconfig.ServiceConfig{Port: 3000},
	}, DefaultNamespace, TLSConfig{})
	if err != nil {
		t.Fatalf("generate ingress: %v", err)
	}
	if ok {
		t.Fatal("expected empty domain to skip ingress")
	}
}

func TestManifestForAppIncludesTLSForConfiguredACME(t *testing.T) {
	manifest, ok, err := ManifestForApp(appconfig.Config{
		Name:    "my-api",
		Service: appconfig.ServiceConfig{Port: 3000},
		Routing: appconfig.RoutingConfig{Domain: "api.example.com"},
	}, DefaultNamespace, TLSConfig{ACMEEmail: "ops@example.com"})
	if err != nil {
		t.Fatalf("generate TLS ingress: %v", err)
	}
	if !ok {
		t.Fatal("expected ingress manifest")
	}
	if manifest.Metadata.Annotations["cert-manager.io/cluster-issuer"] != DefaultClusterIssuer {
		t.Fatalf("unexpected annotations: %#v", manifest.Metadata.Annotations)
	}
	if len(manifest.Spec.TLS) != 1 || manifest.Spec.TLS[0].SecretName != "my-api-tls" {
		t.Fatalf("unexpected TLS configuration: %#v", manifest.Spec.TLS)
	}
}
