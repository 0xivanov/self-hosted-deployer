package server

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAppServiceDeployUpdateListInspectAndDelete(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	deployments := db.NewDeploymentRepository(database)
	routes := db.NewRouteRepository(database)
	runtime := &recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}
	service := NewAppService(AppServiceConfig{
		Apps:            apps,
		Deployments:     deployments,
		Routes:          routes,
		Runtime:         runtime,
		RouteTLSEnabled: true,
	})

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
	route, err := routes.FindByDomain(ctx, "api.example.com")
	if err != nil {
		t.Fatalf("find route: %v", err)
	}
	if route.AppID != first.GetApp().GetId() || route.TargetPort != 3000 || route.Status != "pending" || !route.TLSEnabled {
		t.Fatalf("unexpected route: %#v", route)
	}
	if len(runtime.reconciled) != 2 || runtime.reconciled[1].Routing.Domain != "api.example.com" {
		t.Fatalf("expected app resource reconciles, got %#v", runtime.reconciled)
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
	if len(inspected.GetRoutes()) != 1 || inspected.GetRoutes()[0].GetDomain() != "api.example.com" ||
		inspected.GetRoutes()[0].GetStatus() != "healthy" || !inspected.GetRoutes()[0].GetTlsEnabled() {
		t.Fatalf("expected app route in inspect response, got %#v", inspected.GetRoutes())
	}

	listedRoutes, err := service.ListRoutes(ctx, &deployerv1.ListRoutesRequest{})
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(listedRoutes.GetRoutes()) != 1 || listedRoutes.GetRoutes()[0].GetDomain() != "api.example.com" {
		t.Fatalf("unexpected routes list: %#v", listedRoutes)
	}
	inspectedRoute, err := service.InspectRoute(ctx, &deployerv1.InspectRouteRequest{Domain: "api.example.com"})
	if err != nil {
		t.Fatalf("inspect route: %v", err)
	}
	if inspectedRoute.GetRoute().GetAppId() != first.GetApp().GetId() {
		t.Fatalf("unexpected inspected route: %#v", inspectedRoute)
	}

	if _, err := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "my-api"}); err != nil {
		t.Fatalf("delete app: %v", err)
	}
	if _, err := service.InspectApp(ctx, &deployerv1.InspectAppRequest{Name: "my-api"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected deleted app to be hidden, got %v", err)
	}
	if _, err := routes.FindByDomain(ctx, "api.example.com"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected delete to remove app route, got %v", err)
	}
	if len(runtime.deleted) != 1 || runtime.deleted[0] != "my-api" {
		t.Fatalf("expected deleted app resources, got %#v", runtime.deleted)
	}
}

func TestAppServiceStatusReportsResilienceWarnings(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &recordingAppRuntime{
		status:            "degraded",
		desiredReplicas:   2,
		availableReplicas: 1,
		runningNodes:      []string{"pi-kitchen"},
	}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	yaml := testAppYAML("ivan/my-api:1.0.0", 1) + "resilience:\n  mode: resilient\n"
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: yaml}); err != nil {
		t.Fatalf("deploy resilient app: %v", err)
	}
	got, err := service.GetAppStatus(ctx, &deployerv1.GetAppStatusRequest{Name: "my-api"})
	if err != nil {
		t.Fatalf("get app status: %v", err)
	}
	if got.GetRuntimeStatus() != "degraded" || got.GetDesiredReplicas() != 2 || got.GetAvailableReplicas() != 1 ||
		len(got.GetRunningNodes()) != 1 || len(got.GetWarnings()) != 2 {
		t.Fatalf("unexpected resilience status: %#v", got)
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

func TestAppServiceDeletesRouteWhenDomainRemoved(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	routes := db.NewRouteRepository(database)
	runtime := &recordingAppRuntime{status: "healthy"}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      routes,
		Runtime:     runtime,
	})

	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)}); err != nil {
		t.Fatalf("deploy with route: %v", err)
	}
	if _, err := routes.FindByDomain(ctx, "api.example.com"); err != nil {
		t.Fatalf("expected route: %v", err)
	}

	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAMLWithoutDomain("ivan/my-api:1.0.1", 2)}); err != nil {
		t.Fatalf("deploy without route: %v", err)
	}
	if _, err := routes.FindByDomain(ctx, "api.example.com"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected route to be deleted, got %v", err)
	}
	if len(runtime.reconciled) != 2 || runtime.reconciled[1].Routing.Domain != "" {
		t.Fatalf("expected resource reconciliation without domain, got %#v", runtime.reconciled)
	}
}

func TestAppServiceUpdatesRouteDomain(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	routes := db.NewRouteRepository(database)
	runtime := &recordingAppRuntime{status: "healthy"}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      routes,
		Runtime:     runtime,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)}); err != nil {
		t.Fatalf("deploy original domain: %v", err)
	}
	updatedYAML := strings.Replace(testAppYAML("ivan/my-api:1.0.1", 2), "api.example.com", "new.example.com", 1)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: updatedYAML}); err != nil {
		t.Fatalf("deploy new domain: %v", err)
	}
	if _, err := routes.FindByDomain(ctx, "api.example.com"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected original domain removed, got %v", err)
	}
	route, err := routes.FindByDomain(ctx, "new.example.com")
	if err != nil || route.AppID == "" {
		t.Fatalf("expected new route domain, got %#v: %v", route, err)
	}
	if runtime.reconciled[1].Routing.Domain != "new.example.com" {
		t.Fatalf("expected updated route host, got %#v", runtime.reconciled)
	}
}

func TestAppServiceRefreshesRouteHealth(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}
	eventRecorder := &recordingEventRecorder{}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
		Events:      eventRecorder,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)}); err != nil {
		t.Fatalf("deploy app: %v", err)
	}
	listed, err := service.ListRoutes(ctx, &deployerv1.ListRoutesRequest{})
	if err != nil || listed.GetRoutes()[0].GetStatus() != "healthy" {
		t.Fatalf("expected healthy route, got %#v: %v", listed, err)
	}

	runtime.status = "unavailable"
	runtime.availableReplicas = 0
	listed, err = service.ListRoutes(ctx, &deployerv1.ListRoutesRequest{})
	if err != nil || listed.GetRoutes()[0].GetStatus() != "unavailable" {
		t.Fatalf("expected unavailable route, got %#v: %v", listed, err)
	}
	runtime.status = "healthy"
	runtime.availableReplicas = 2
	if _, err := service.ListRoutes(ctx, &deployerv1.ListRoutesRequest{}); err != nil {
		t.Fatalf("refresh recovered route: %v", err)
	}
	for _, eventType := range []domain.EventType{
		domain.EventTypeAppDeployStarted,
		domain.EventTypeAppDeploySucceeded,
		domain.EventTypeRouteDegraded,
		domain.EventTypeAppHealthDegraded,
		domain.EventTypeRouteRecovered,
		domain.EventTypeAppHealthRecovered,
	} {
		if !eventRecorder.hasType(eventType) {
			t.Fatalf("expected event %s, got %#v", eventType, eventRecorder.events)
		}
	}
	for _, event := range eventRecorder.events {
		if event.Type == domain.EventTypeAppHealthDegraded && !strings.Contains(event.MetadataJSON, `"desired_replicas":2`) {
			t.Fatalf("expected app health metadata to contain replica counts, got %s", event.MetadataJSON)
		}
	}
}

func TestAppServiceRejectsDomainOwnedByAnotherAppBeforeIngress(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &recordingAppRuntime{status: "healthy"}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)}); err != nil {
		t.Fatalf("deploy first app: %v", err)
	}
	otherAppYAML := strings.Replace(testAppYAML("ivan/other-api:1.0.0", 2), "name: my-api", "name: other-api", 1)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: otherAppYAML}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected duplicate domain conflict, got %v", err)
	}
	if len(runtime.reconciled) != 1 {
		t.Fatalf("expected no conflicting app reconcile, got %#v", runtime.reconciled)
	}
}

func TestAppServiceRecordsResourceApplyFailure(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	deployments := db.NewDeploymentRepository(database)
	events := &recordingEventRecorder{}
	service := NewAppService(AppServiceConfig{
		Apps:        apps,
		Deployments: deployments,
		Routes:      db.NewRouteRepository(database),
		Runtime:     &recordingAppRuntime{err: errors.New("apply failed")},
		Events:      events,
	})

	_, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected resource apply failure, got %v", err)
	}
	app, err := apps.FindByName(ctx, "my-api")
	if err != nil {
		t.Fatalf("find app: %v", err)
	}
	records, err := deployments.ListByApp(ctx, app.ID)
	if err != nil || len(records) != 1 || records[0].Status != "failed" {
		t.Fatalf("expected failed deployment record, got %#v: %v", records, err)
	}
	if !events.hasType(domain.EventTypeAppDeployStarted) || !events.hasType(domain.EventTypeAppDeployFailed) {
		t.Fatalf("expected failed deploy events, got %#v", events.events)
	}
	for _, event := range events.events {
		if event.Type == domain.EventTypeAppDeployFailed && !strings.Contains(event.MetadataJSON, "apply failed") {
			t.Fatalf("expected failure reason in metadata, got %s", event.MetadataJSON)
		}
	}
}

func TestAppServiceRecordsRouteSyncFailure(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	deployments := db.NewDeploymentRepository(database)
	routes := &failingRouteRepository{
		RouteRepository: db.NewRouteRepository(database),
		upsertErr:       errors.New("write failed"),
	}
	service := NewAppService(AppServiceConfig{
		Apps:        apps,
		Deployments: deployments,
		Routes:      routes,
	})

	_, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected route sync failure, got %v", err)
	}
	app, err := apps.FindByName(ctx, "my-api")
	if err != nil {
		t.Fatalf("find app: %v", err)
	}
	records, err := deployments.ListByApp(ctx, app.ID)
	if err != nil || len(records) != 1 || records[0].Status != "failed" || records[0].FailureReason == "" {
		t.Fatalf("expected failed deployment record, got %#v: %v", records, err)
	}
}

func TestAppServiceCanRetryDeleteAfterRouteCleanupFailure(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	routes := &failingRouteRepository{RouteRepository: db.NewRouteRepository(database)}
	service := NewAppService(AppServiceConfig{
		Apps:        apps,
		Deployments: db.NewDeploymentRepository(database),
		Routes:      routes,
	})

	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)}); err != nil {
		t.Fatalf("deploy app: %v", err)
	}
	routes.deleteErr = errors.New("write failed")
	if _, err := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "my-api"}); status.Code(err) != codes.Internal {
		t.Fatalf("expected route delete failure, got %v", err)
	}
	if _, err := apps.FindActiveByName(ctx, "my-api"); err != nil {
		t.Fatalf("expected app to remain active for retry, got %v", err)
	}

	routes.deleteErr = nil
	if _, err := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "my-api"}); err != nil {
		t.Fatalf("retry delete app: %v", err)
	}
	if _, err := routes.FindByDomain(ctx, "api.example.com"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected retry to remove route, got %v", err)
	}
}

func TestAppServiceRequiresAndPassesReferencedSecretsToRuntime(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	secrets := db.NewSecretRepository(database)
	cipher, err := security.NewSecretCipher([]byte(strings.Repeat("s", security.SecretKeyBytes)))
	if err != nil {
		t.Fatalf("new secret cipher: %v", err)
	}
	runtime := &recordingAppRuntime{}
	service := NewAppService(AppServiceConfig{
		Apps:        apps,
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Secrets:     secrets,
		Cipher:      cipher,
		Runtime:     runtime,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)}); err != nil {
		t.Fatalf("deploy initial app: %v", err)
	}
	secretYAML := strings.Replace(testAppYAML("ivan/my-api:1.0.1", 2), "state:\n", "secrets:\n  - DATABASE_URL\nstate:\n", 1)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: secretYAML}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected missing required secret failure, got %v", err)
	}
	app, err := apps.FindByName(ctx, "my-api")
	if err != nil {
		t.Fatalf("find app: %v", err)
	}
	encrypted, err := cipher.Encrypt("postgres://app-db")
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	now := service.now().UTC()
	if err := secrets.Set(ctx, domain.Secret{AppID: app.ID, Name: "DATABASE_URL", Ciphertext: encrypted, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("set stored secret: %v", err)
	}
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: secretYAML}); err != nil {
		t.Fatalf("deploy app with secret: %v", err)
	}
	if got := runtime.secretValues[len(runtime.secretValues)-1]["DATABASE_URL"]; got != "postgres://app-db" {
		t.Fatalf("runtime received unexpected secret value %q", got)
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

func testAppYAMLWithoutDomain(image string, replicas int) string {
	return `name: my-api
image: ` + image + `
service:
  port: 3000
  health:
    path: /health
routing: {}
deploy:
  replicas: ` + strconv.Itoa(replicas) + `
placement:
  arch: linux/arm64
  spread: true
state:
  mode: stateless
`
}

type recordingAppRuntime struct {
	reconciled        []appconfig.Config
	secretValues      []map[string]string
	secretRevisions   []string
	deleted           []string
	status            string
	desiredReplicas   int32
	availableReplicas int32
	runningNodes      []string
	err               error
}

func (r *recordingAppRuntime) Reconcile(_ context.Context, cfg appconfig.Config, secretValues map[string]string, secretRevision string) error {
	r.reconciled = append(r.reconciled, cfg)
	r.secretValues = append(r.secretValues, secretValues)
	r.secretRevisions = append(r.secretRevisions, secretRevision)
	return r.err
}

func (r *recordingAppRuntime) Delete(_ context.Context, appName string) error {
	r.deleted = append(r.deleted, appName)
	return r.err
}

func (r *recordingAppRuntime) Status(context.Context, string) (string, error) {
	return r.status, r.err
}

func (r *recordingAppRuntime) StatusDetails(context.Context, string) (string, int32, int32, error) {
	return r.status, r.desiredReplicas, r.availableReplicas, r.err
}

func (r *recordingAppRuntime) RuntimeStatus(context.Context, string) (string, int32, int32, []string, error) {
	return r.status, r.desiredReplicas, r.availableReplicas, append([]string(nil), r.runningNodes...), r.err
}

type failingRouteRepository struct {
	RouteRepository
	upsertErr error
	deleteErr error
}

func (r *failingRouteRepository) UpsertForApp(ctx context.Context, route domain.Route) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	return r.RouteRepository.UpsertForApp(ctx, route)
}

func (r *failingRouteRepository) DeleteByApp(ctx context.Context, appID string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	return r.RouteRepository.DeleteByApp(ctx, appID)
}
