package server

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeployRejectsHostingProfileOmissionBeforeMutation(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &hostingTestRuntime{recordingAppRuntime: recordingAppRuntime{status: "healthy"}}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	first, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: hostingDeployYAML()})
	if err != nil {
		t.Fatalf("initial hosted deploy: %v", err)
	}
	withoutProfile := strings.Replace(hostingDeployYAML(), hostingBlock(), "", 1)
	_, err = service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: withoutProfile})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "hosting profile cannot be omitted") {
		t.Fatalf("expected profile omission rejection, got %v", err)
	}
	if len(runtime.reconciled) != 1 {
		t.Fatalf("runtime was mutated before profile omission rejection: %d reconciles", len(runtime.reconciled))
	}
	deployments, err := db.NewDeploymentRepository(database).ListByApp(ctx, first.GetApp().GetId())
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 1 {
		t.Fatalf("deployment record was created before rejection: %d", len(deployments))
	}
	stored, err := db.NewAppRepository(database).FindActiveByName(ctx, "hosted-api")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.DesiredStateJSON, `"hosting"`) {
		t.Fatalf("stored hosting profile was changed by rejected update: %s", stored.DesiredStateJSON)
	}
}

func hostingBlock() string {
	return `hosting:
  version: v1
  maxReplicas: 3
  resources:
    requests: {cpu: 100m, memory: 128Mi, ephemeralStorage: 1Gi}
    limits: {cpu: 500m, memory: 512Mi, ephemeralStorage: 2Gi}
`
}

func hostingDeployYAML() string {
	return `name: hosted-api
image: example/hosted-api:1.0.0
service:
  port: 8080
  health:
    path: /health
routing: {}
deploy:
  replicas: 2
placement: {}
` + hostingBlock()
}

type hostingTestRuntime struct{ recordingAppRuntime }

func (*hostingTestRuntime) PreflightHosting(context.Context, appconfig.Config) error { return nil }

func TestUnsupportedHostingRuntimeFailsBeforePersistence(t *testing.T) {
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	runtime := &recordingAppRuntime{status: "healthy"}
	service := NewAppService(AppServiceConfig{Apps: apps, Runtime: runtime})
	_, err := service.DeployApp(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.DeployAppRequest{DeployerYaml: hostingDeployYAML()})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected unsupported runtime, got %v", err)
	}
	stored, err := apps.List(context.Background())
	if err != nil || len(stored) != 0 || len(runtime.reconciled) != 0 {
		t.Fatalf("preflight mutated app state: %v", err)
	}
}
