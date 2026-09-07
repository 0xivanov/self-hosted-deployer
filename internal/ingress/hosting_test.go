package ingress

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	corev1 "k8s.io/api/core/v1"
)

func hostingTestConfig(t *testing.T) appconfig.Config {
	t.Helper()
	cfg, err := appconfig.Parse([]byte(`
name: hosted-api
image: example/hosted-api:1.0.0
service:
  port: 8080
  health:
    path: /health
routing: {}
deploy:
  replicas: 2
placement: {}
hosting:
  version: v1
  maxReplicas: 3
  resources:
    requests: {cpu: 100m, memory: 128Mi, ephemeralStorage: 1Gi}
    limits: {cpu: 500m, memory: 512Mi, ephemeralStorage: 2Gi}
`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHostingProfileHardensPodsAndSetsResources(t *testing.T) {
	deployment, err := deploymentForApp(hostingTestConfig(t), DefaultNamespace, "")
	if err != nil {
		t.Fatal(err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 2 {
		t.Fatalf("unexpected replicas: %v", deployment.Spec.Replicas)
	}
	cpuRequest := container.Resources.Requests[corev1.ResourceCPU]
	memoryLimit := container.Resources.Limits[corev1.ResourceMemory]
	if cpuRequest.String() != "100m" || memoryLimit.String() != "512Mi" {
		t.Fatalf("unexpected resources: %#v", container.Resources)
	}
	security := container.SecurityContext
	if security == nil || security.RunAsNonRoot == nil || !*security.RunAsNonRoot || security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation {
		t.Fatalf("hosting security context is not hardened: %#v", security)
	}
	if len(security.Capabilities.Drop) != 1 || security.Capabilities.Drop[0] != "ALL" || security.SeccompProfile == nil || security.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("hosting capability/seccomp policy is incomplete: %#v", security)
	}
	if deployment.Spec.Template.Spec.AutomountServiceAccountToken == nil || *deployment.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatalf("service account token automount must be disabled")
	}
}

func TestHostingProfilePreflightRejectsUnsupportedRuntimeBeforeApply(t *testing.T) {
	cfg := hostingTestConfig(t)
	controller := &Controller{namespace: DefaultNamespace}
	err := controller.Reconcile(context.Background(), cfg, nil, "")
	if err == nil || !strings.Contains(err.Error(), "hosting profile v1 is unsupported") {
		t.Fatalf("expected unsupported runtime rejection, got %v", err)
	}
}
