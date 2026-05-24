package server

import (
	"context"
	"strconv"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAppServiceDeployUpdateListInspectAndDelete(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	deployments := db.NewDeploymentRepository(database)
	service := NewAppService(AppServiceConfig{Apps: apps, Deployments: deployments})

	first, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)})
	if err != nil {
		t.Fatalf("deploy app: %v", err)
	}
	if first.GetApp().GetName() != "my-api" || first.GetDeployment().GetStatus() != "pending" {
		t.Fatalf("unexpected deploy response: %#v", first)
	}

	updated, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.1", 3)})
	if err != nil {
		t.Fatalf("update app: %v", err)
	}
	if updated.GetApp().GetId() != first.GetApp().GetId() || updated.GetApp().GetImage() != "ivan/my-api:1.0.1" {
		t.Fatalf("expected app update, got %#v", updated)
	}
	if updated.GetDeployment().GetId() == first.GetDeployment().GetId() {
		t.Fatalf("expected a new deployment record for update")
	}

	listed, err := service.ListApps(ctx, &deployerv1.ListAppsRequest{})
	if err != nil {
		t.Fatalf("list apps: %v", err)
	}
	if len(listed.GetApps()) != 1 || listed.GetApps()[0].GetReplicas() != 3 {
		t.Fatalf("unexpected app list: %#v", listed)
	}

	inspected, err := service.InspectApp(ctx, &deployerv1.InspectAppRequest{Name: "my-api"})
	if err != nil {
		t.Fatalf("inspect app: %v", err)
	}
	if len(inspected.GetDeployments()) != 2 {
		t.Fatalf("expected two deployments, got %#v", inspected.GetDeployments())
	}

	if _, err := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "my-api"}); err != nil {
		t.Fatalf("delete app: %v", err)
	}
	if _, err := service.InspectApp(ctx, &deployerv1.InspectAppRequest{Name: "my-api"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected deleted app to be hidden, got %v", err)
	}
}

func TestAppServiceRequiresAdminCaller(t *testing.T) {
	service := NewAppService(AppServiceConfig{})
	_, err := service.ListApps(context.Background(), &deployerv1.ListAppsRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestAppServiceDeployRejectsInvalidConfig(t *testing.T) {
	service := NewAppService(AppServiceConfig{})
	_, err := service.DeployApp(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.DeployAppRequest{
		DeployerYaml: "name: My_API\n",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func testAppYAML(image string, replicas int) string {
	return `name: my-api
image: ` + image + `
service:
  port: 3000
  health:
    path: /health
routing:
  domain: api.example.com
deploy:
  replicas: ` + strconv.Itoa(replicas) + `
placement:
  arch: linux/arm64
  spread: true
state:
  mode: stateless
`
}
