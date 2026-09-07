package server

import (
	"context"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"testing"
)

func TestPreflightDoesNotPersistOrReconcile(t *testing.T) {
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	runtime := &hostingTestRuntime{}
	service := NewAppService(AppServiceConfig{Apps: apps, Routes: db.NewRouteRepository(database), Runtime: runtime})
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	result, err := service.PreflightApp(ctx, &deployerv1.PreflightAppRequest{DeployerYaml: hostingDeployYAML()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.GetDesiredState(), `"hosting"`) || len(result.GetWarnings()) == 0 {
		t.Fatalf("incomplete report: %v", result)
	}
	stored, err := apps.List(ctx)
	if err != nil || len(stored) != 0 || len(runtime.reconciled) != 0 {
		t.Fatalf("preflight mutated state: %v", err)
	}
	_, err = service.PreflightApp(context.Background(), &deployerv1.PreflightAppRequest{DeployerYaml: hostingDeployYAML()})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous preflight: %v", err)
	}
}

func TestPreflightRejectsHostingProfileRemoval(t *testing.T) {
	database := openTestDB(t)
	runtime := &hostingTestRuntime{}
	service := NewAppService(AppServiceConfig{Apps: db.NewAppRepository(database), Deployments: db.NewDeploymentRepository(database), Routes: db.NewRouteRepository(database), Runtime: runtime})
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	initial, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: hostingDeployYAML()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.PreflightApp(ctx, &deployerv1.PreflightAppRequest{DeployerYaml: strings.Replace(hostingDeployYAML(), hostingBlock(), "", 1)})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected omission rejection: %v", err)
	}
	deployments, err := db.NewDeploymentRepository(database).ListByApp(ctx, initial.GetApp().GetId())
	if err != nil || len(deployments) != 1 || len(runtime.reconciled) != 1 {
		t.Fatalf("preflight mutated state: %v", err)
	}
}

func TestPreflightRejectsDomainConflictWithoutMutation(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &recordingAppRuntime{status: "healthy"}
	apps := db.NewAppRepository(database)
	service := NewAppService(AppServiceConfig{Apps: apps, Deployments: db.NewDeploymentRepository(database), Routes: db.NewRouteRepository(database), Runtime: runtime})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("example/api:v1", 2)}); err != nil {
		t.Fatal(err)
	}
	yaml := strings.Replace(testAppYAML("example/other:v1", 2), "name: my-api", "name: other-api", 1)
	if _, err := service.PreflightApp(ctx, &deployerv1.PreflightAppRequest{DeployerYaml: yaml}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected domain conflict: %v", err)
	}
	stored, err := apps.List(ctx)
	if err != nil || len(stored) != 1 || len(runtime.reconciled) != 1 {
		t.Fatalf("preflight mutated state: %v", err)
	}
}
