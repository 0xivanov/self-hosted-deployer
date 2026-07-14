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
	"google.golang.org/grpc"
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
	if first.GetApp().GetName() != "my-api" || first.GetDeployment().GetStatus() != "healthy" {
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

func TestAppServiceStatusReportsManagedPostgres(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &recordingAppRuntime{
		status:            "healthy",
		desiredReplicas:   1,
		availableReplicas: 1,
		runningNodes:      []string{"pi-home"},
		databaseStatus:    healthyTestDatabaseStatus(),
	}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testPostgresAppYAML()}); err != nil {
		t.Fatalf("deploy app with managed postgres: %v", err)
	}

	got, err := service.GetAppStatus(ctx, &deployerv1.GetAppStatusRequest{Name: "my-api"})
	if err != nil {
		t.Fatalf("get app status: %v", err)
	}
	databaseStatus := got.GetDatabase()
	if databaseStatus == nil || databaseStatus.GetState() != "healthy" || databaseStatus.GetPhase() != "Cluster in healthy state" ||
		databaseStatus.GetDesiredInstances() != 3 || databaseStatus.GetReadyInstances() != 3 ||
		databaseStatus.GetPrimary() != "my-api-db-1" || len(databaseStatus.GetRunningNodes()) != 3 {
		t.Fatalf("unexpected database status: %#v", databaseStatus)
	}
	if runtime.databaseStatusAppName != "my-api" || len(got.GetWarnings()) != 0 {
		t.Fatalf("unexpected runtime app=%q warnings=%#v", runtime.databaseStatusAppName, got.GetWarnings())
	}
}

func TestAppServiceStatusTreatsEquivalentPostgresStorageQuantitiesAsEqual(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &recordingAppRuntime{
		status:            "healthy",
		desiredReplicas:   1,
		availableReplicas: 1,
		databaseStatus:    healthyTestDatabaseStatus(),
	}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	yaml := strings.Replace(testPostgresAppYAML(), "size: 1Gi", "size: 1024Mi", 1)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: yaml}); err != nil {
		t.Fatalf("deploy app with equivalent storage quantity: %v", err)
	}

	got, err := service.GetAppStatus(ctx, &deployerv1.GetAppStatusRequest{Name: "my-api"})
	if err != nil {
		t.Fatalf("get app status: %v", err)
	}
	if got.GetDatabase().GetState() != "healthy" || len(got.GetWarnings()) != 0 {
		t.Fatalf("equivalent storage quantities must remain healthy, got %#v", got)
	}
}

func TestAppServiceStatusDegradesOnPostgresHAPolicyDrift(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	databaseStatus := healthyTestDatabaseStatus()
	databaseStatus.SynchronousMethod = "first"
	runtime := &recordingAppRuntime{
		status:            "healthy",
		desiredReplicas:   1,
		availableReplicas: 1,
		databaseStatus:    databaseStatus,
	}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testPostgresAppYAML()}); err != nil {
		t.Fatalf("deploy app with managed postgres: %v", err)
	}

	got, err := service.GetAppStatus(ctx, &deployerv1.GetAppStatusRequest{Name: "my-api"})
	if err != nil {
		t.Fatalf("get app status: %v", err)
	}
	if got.GetDatabase().GetState() != "degraded" || !strings.Contains(strings.Join(got.GetWarnings(), "\n"), `synchronous method is "first"; expected "any"`) {
		t.Fatalf("expected HA drift to degrade database status, got %#v", got)
	}
}

func TestAppServiceStatusDegradesOnStaleFailoverQuorumStandbys(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	databaseStatus := healthyTestDatabaseStatus()
	databaseStatus.FailoverQuorumStandbyNames = []string{"my-api-db-4", "my-api-db-5"}
	runtime := &recordingAppRuntime{
		status:            "healthy",
		desiredReplicas:   1,
		availableReplicas: 1,
		databaseStatus:    databaseStatus,
	}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testPostgresAppYAML()}); err != nil {
		t.Fatalf("deploy app with managed postgres: %v", err)
	}

	got, err := service.GetAppStatus(ctx, &deployerv1.GetAppStatusRequest{Name: "my-api"})
	if err != nil {
		t.Fatalf("get app status: %v", err)
	}
	warnings := strings.Join(got.GetWarnings(), "\n")
	if got.GetDatabase().GetState() != "degraded" ||
		!strings.Contains(warnings, "expected current running replicas my-api-db-2, my-api-db-3") {
		t.Fatalf("expected stale FailoverQuorum names to degrade status, got %#v", got)
	}
}

func TestAppServiceDatabaseStatusFailureDoesNotHideAppStatus(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &recordingAppRuntime{
		status:            "healthy",
		desiredReplicas:   1,
		availableReplicas: 1,
		databaseStatusErr: errors.New("CloudNativePG operator CRD is not installed"),
	}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testPostgresAppYAML()}); err != nil {
		t.Fatalf("deploy app with managed postgres: %v", err)
	}

	got, err := service.GetAppStatus(ctx, &deployerv1.GetAppStatusRequest{Name: "my-api"})
	if err != nil {
		t.Fatalf("database status failure must not hide app status: %v", err)
	}
	if got.GetRuntimeStatus() != "healthy" || got.GetDatabase().GetState() != "unknown" ||
		got.GetDatabase().GetDesiredInstances() != 3 || len(got.GetWarnings()) != 1 ||
		!strings.Contains(got.GetWarnings()[0], "CloudNativePG operator CRD is not installed") {
		t.Fatalf("unexpected status after database reporting failure: %#v", got)
	}
}

func TestDatabaseRuntimeStateAndWarnings(t *testing.T) {
	tests := []struct {
		name        string
		status      domain.DatabaseStatus
		wantState   string
		wantWarning string
	}{
		{name: "missing", status: domain.DatabaseStatus{}, wantState: "missing", wantWarning: "not present"},
		{name: "not ready", status: domain.DatabaseStatus{Present: true}, wantState: "not_ready", wantWarning: "0 of 3"},
		{name: "degraded", status: domain.DatabaseStatus{Present: true, Phase: "Waiting for instances", ReadyInstances: 2, Primary: "db-1"}, wantState: "degraded", wantWarning: "2 of 3"},
		{name: "not spread", status: domain.DatabaseStatus{Present: true, Phase: "Cluster in healthy state", ReadyInstances: 3, Primary: "db-1", RunningNodes: []string{"node-a", "node-b"}}, wantState: "degraded", wantWarning: "2 of 3 required nodes"},
		{name: "too many ready", status: domain.DatabaseStatus{Present: true, Phase: "Cluster in healthy state", ReadyInstances: 4, Primary: "db-1", RunningNodes: []string{"node-a", "node-b", "node-c"}}, wantState: "degraded", wantWarning: "reports 4 ready instances; expected 3"},
		{name: "too many nodes", status: domain.DatabaseStatus{Present: true, Phase: "Cluster in healthy state", ReadyInstances: 3, Primary: "db-1", RunningNodes: []string{"node-a", "node-b", "node-c", "node-d"}}, wantState: "degraded", wantWarning: "running on 4 nodes; expected 3"},
		{name: "phase missing", status: domain.DatabaseStatus{Present: true, ReadyInstances: 3, Primary: "db-1", RunningNodes: []string{"node-a", "node-b", "node-c"}}, wantState: "degraded", wantWarning: "phase is not reported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := databaseRuntimeState(tt.status, 3); got != tt.wantState {
				t.Fatalf("state: got %q want %q", got, tt.wantState)
			}
			if got := strings.Join(databaseRuntimeWarnings(tt.status, 3), "\n"); !strings.Contains(got, tt.wantWarning) {
				t.Fatalf("warnings %q do not contain %q", got, tt.wantWarning)
			}
		})
	}
}

func TestDatabaseHAPolicyWarnings(t *testing.T) {
	cfg, err := appconfig.Parse([]byte(testPostgresAppYAML()))
	if err != nil {
		t.Fatalf("parse test config: %v", err)
	}

	tests := []struct {
		name       string
		mutate     func(*domain.DatabaseStatus)
		wantDetail string
	}{
		{name: "ownership", mutate: func(status *domain.DatabaseStatus) { status.OwnedByDeployer = false }, wantDetail: "ownership labels do not match"},
		{name: "image", mutate: func(status *domain.DatabaseStatus) { status.Image = "other" }, wantDetail: `image is "other"; expected`},
		{name: "bootstrap database", mutate: func(status *domain.DatabaseStatus) { status.BootstrapDatabase = "other" }, wantDetail: `bootstrap database is "other"; expected "money_manager"`},
		{name: "bootstrap owner", mutate: func(status *domain.DatabaseStatus) { status.BootstrapOwner = "other" }, wantDetail: `bootstrap owner is "other"; expected "money_manager"`},
		{name: "data checksums", mutate: func(status *domain.DatabaseStatus) { status.DataChecksumsEnabled = false }, wantDetail: "data checksums are not enabled"},
		{name: "storage size", mutate: func(status *domain.DatabaseStatus) { status.StorageSize = "2Gi" }, wantDetail: `storage size is "2Gi"; expected "1Gi"`},
		{name: "storage class", mutate: func(status *domain.DatabaseStatus) { status.StorageClass = "other" }, wantDetail: `storage class is "other"; expected "local-path"`},
		{name: "instance count", mutate: func(status *domain.DatabaseStatus) { status.DesiredInstances = 2 }, wantDetail: "specifies 2 instances; expected 3"},
		{name: "synchronous method", mutate: func(status *domain.DatabaseStatus) { status.SynchronousMethod = "first" }, wantDetail: `synchronous method is "first"; expected "any"`},
		{name: "synchronous replicas", mutate: func(status *domain.DatabaseStatus) { status.SynchronousReplicas = 2 }, wantDetail: "synchronous replica count is 2; expected 1"},
		{name: "data durability", mutate: func(status *domain.DatabaseStatus) { status.DataDurability = "preferred" }, wantDetail: `data durability is "preferred"; expected "required"`},
		{name: "failover disabled", mutate: func(status *domain.DatabaseStatus) { status.FailoverQuorumEnabled = false }, wantDetail: "failover quorum is disabled"},
		{name: "anti-affinity", mutate: func(status *domain.DatabaseStatus) { status.AntiAffinityType = "preferred" }, wantDetail: `pod anti-affinity type is "preferred"; expected "required"`},
		{name: "topology", mutate: func(status *domain.DatabaseStatus) { status.TopologyKey = "topology.kubernetes.io/zone" }, wantDetail: `topology key is "topology.kubernetes.io/zone"; expected "kubernetes.io/hostname"`},
		{name: "architecture", mutate: func(status *domain.DatabaseStatus) { status.Architecture = "amd64" }, wantDetail: `placement architecture is "amd64"; expected "arm64"`},
		{name: "password encryption", mutate: func(status *domain.DatabaseStatus) { status.PasswordEncryption = "md5" }, wantDetail: `password encryption is "md5"; expected "scram-sha-256"`},
		{name: "non TLS allowed", mutate: func(status *domain.DatabaseStatus) { status.RejectsNonTLS = false }, wantDetail: "does not reject non-TLS"},
		{name: "application SCRAM absent", mutate: func(status *domain.DatabaseStatus) { status.RequiresApplicationSCRAM = false }, wantDetail: "does not require SCRAM-SHA-256 over TLS"},
		{name: "quorum absent", mutate: func(status *domain.DatabaseStatus) { status.FailoverQuorumPresent = false }, wantDetail: "FailoverQuorum status object is not present"},
		{name: "quorum method", mutate: func(status *domain.DatabaseStatus) { status.FailoverQuorumMethod = "first" }, wantDetail: `FailoverQuorum method is "first"; expected "any"`},
		{name: "quorum standby number", mutate: func(status *domain.DatabaseStatus) { status.FailoverQuorumStandbyNumber = 2 }, wantDetail: "FailoverQuorum standby number is 2; expected 1"},
		{name: "quorum primary missing", mutate: func(status *domain.DatabaseStatus) { status.FailoverQuorumPrimary = "" }, wantDetail: "FailoverQuorum primary is not reported"},
		{name: "quorum primary stale", mutate: func(status *domain.DatabaseStatus) { status.FailoverQuorumPrimary = "my-api-db-old" }, wantDetail: `FailoverQuorum primary is "my-api-db-old"; current primary is "my-api-db-1"`},
		{name: "quorum standbys insufficient", mutate: func(status *domain.DatabaseStatus) { status.FailoverQuorumStandbyNames = []string{"my-api-db-2"} }, wantDetail: "expected current running replicas my-api-db-2, my-api-db-3"},
		{name: "quorum standbys duplicate", mutate: func(status *domain.DatabaseStatus) {
			status.FailoverQuorumStandbyNames = []string{"my-api-db-2", "my-api-db-2"}
		}, wantDetail: "duplicate or empty standby names"},
		{name: "quorum standbys include primary", mutate: func(status *domain.DatabaseStatus) {
			status.FailoverQuorumStandbyNames = []string{"my-api-db-1", "my-api-db-2"}
		}, wantDetail: "standby names include the current primary"},
	}

	if warnings := databaseHAPolicyWarnings(cfg, healthyTestDatabaseStatus(), 3); len(warnings) != 0 {
		t.Fatalf("healthy HA policy produced warnings: %#v", warnings)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := healthyTestDatabaseStatus()
			tt.mutate(&status)
			warnings := strings.Join(databaseHAPolicyWarnings(cfg, status, 3), "\n")
			if !strings.Contains(warnings, tt.wantDetail) {
				t.Fatalf("warnings %q do not contain %q", warnings, tt.wantDetail)
			}
		})
	}
}

func TestDatabaseHAPolicyMissingDuringInitializationIsDegraded(t *testing.T) {
	cfg, err := appconfig.Parse([]byte(testPostgresAppYAML()))
	if err != nil {
		t.Fatalf("parse test config: %v", err)
	}
	status := healthyTestDatabaseStatus()
	status.Phase = "Setting up primary"
	status.ReadyInstances = 0
	status.Primary = ""
	status.RunningNodes = []string{}
	status.FailoverQuorumPresent = false

	warnings := databaseHAPolicyWarnings(cfg, status, 3)
	state := databaseReportedState(status, 3, warnings)
	if state != "degraded" || !strings.Contains(strings.Join(warnings, "\n"), "FailoverQuorum status object is not present") {
		t.Fatalf("expected initializing HA policy to be degraded, state=%q warnings=%#v", state, warnings)
	}
}

func TestAppendDatabaseStatusWarnsWhenConnectionRemainsExternal(t *testing.T) {
	cfg := appconfig.Config{Database: appconfig.DatabaseConfig{Postgres: &appconfig.PostgresConfig{
		Instances:      3,
		ConnectionMode: appconfig.PostgresConnectionModeExternal,
	}}}
	response := &deployerv1.GetAppStatusResponse{}

	appendDatabaseStatus(context.Background(), nil, cfg, "my-api", response)

	if response.GetDatabase().GetState() != "unknown" || response.GetDatabase().GetDesiredInstances() != 3 {
		t.Fatalf("unexpected database fallback status: %#v", response.GetDatabase())
	}
	warnings := strings.Join(response.GetWarnings(), "\n")
	if !strings.Contains(warnings, "external mode") || !strings.Contains(warnings, "runtime status is unavailable") {
		t.Fatalf("unexpected external database warnings: %q", warnings)
	}
}

func TestAppServiceStreamsDeploymentLogsForExistingApp(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	runtime := &recordingAppRuntime{logLines: []string{"booted", "ready"}}
	service := NewAppService(AppServiceConfig{
		Apps:        db.NewAppRepository(database),
		Deployments: db.NewDeploymentRepository(database),
		Routes:      db.NewRouteRepository(database),
		Runtime:     runtime,
	})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1)}); err != nil {
		t.Fatalf("deploy loggable app: %v", err)
	}
	stream := &recordingAppLogStream{ctx: ctx}
	if err := service.GetDeploymentLogs(&deployerv1.GetDeploymentLogsRequest{
		AppName: "my-api", TailLines: 25, Follow: true,
	}, stream); err != nil {
		t.Fatalf("stream app logs: %v", err)
	}
	if runtime.logAppName != "my-api" || runtime.logTailLines != 25 || !runtime.logFollow ||
		len(stream.lines) != 2 || stream.lines[1] != "ready" {
		t.Fatalf("unexpected logs runtime=%#v lines=%#v", runtime, stream.lines)
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
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "apply failed") {
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

func TestFailedManagedPostgresCutoverDoesNotBecomeDesiredState(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	deployments := db.NewDeploymentRepository(database)
	routes := db.NewRouteRepository(database)
	secrets := db.NewSecretRepository(database)
	cipher := newTestSecretCipher(t)
	runtime := &cutoverAppRuntime{}
	appService := NewAppService(AppServiceConfig{
		Apps:        apps,
		Deployments: deployments,
		Routes:      routes,
		Secrets:     secrets,
		Cipher:      cipher,
		Runtime:     runtime,
	})
	if _, err := appService.DeployApp(ctx, &deployerv1.DeployAppRequest{
		DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1),
	}); err != nil {
		t.Fatalf("deploy initial app: %v", err)
	}
	app, err := apps.FindByName(ctx, "my-api")
	if err != nil {
		t.Fatalf("find initial app: %v", err)
	}
	now := appService.now().UTC()
	for name, value := range map[string]string{
		"DATABASE_URL": "postgresql://external",
		"JWT_SECRET":   "jwt-before-cutover",
	} {
		ciphertext, err := cipher.Encrypt(value)
		if err != nil {
			t.Fatalf("encrypt %s: %v", name, err)
		}
		if err := secrets.Set(ctx, domain.Secret{
			AppID: app.ID, Name: name, Ciphertext: ciphertext, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("store %s: %v", name, err)
		}
	}
	externalYAML := testPostgresAppYAMLForCutover(appconfig.PostgresConnectionModeExternal)
	if _, err := appService.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: externalYAML}); err != nil {
		t.Fatalf("deploy external-mode staging config: %v", err)
	}

	runtime.failManaged = true
	managedYAML := testPostgresAppYAMLForCutover(appconfig.PostgresConnectionModeManaged)
	if _, err := appService.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: managedYAML}); status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "managed cutover blocked") {
		t.Fatalf("expected failed managed cutover, got %v", err)
	}
	stored, err := apps.FindByName(ctx, "my-api")
	if err != nil {
		t.Fatalf("find app after failed cutover: %v", err)
	}
	storedConfig, err := appconfig.FromJSON(stored.DesiredStateJSON)
	if err != nil {
		t.Fatalf("decode desired state after failed cutover: %v", err)
	}
	if storedConfig.Database.Postgres == nil ||
		storedConfig.Database.Postgres.ConnectionMode != appconfig.PostgresConnectionModeExternal {
		t.Fatalf("failed managed cutover replaced last applied desired state: %#v", storedConfig.Database.Postgres)
	}

	runtime.failManaged = false
	secretService := NewSecretService(SecretServiceConfig{
		Apps: apps, Secrets: secrets, Cipher: cipher, Runtime: runtime,
	})
	if _, err := secretService.SetSecret(ctx, &deployerv1.SetSecretRequest{
		AppName: "my-api", Name: "JWT_SECRET", Value: "jwt-after-cutover",
	}); err != nil {
		t.Fatalf("update referenced secret after failed cutover: %v", err)
	}
	last := runtime.reconciled[len(runtime.reconciled)-1]
	if last.Database.Postgres == nil ||
		last.Database.Postgres.ConnectionMode != appconfig.PostgresConnectionModeExternal {
		t.Fatalf("secret update retried failed managed cutover: %#v", last.Database.Postgres)
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

func testPostgresAppYAML() string {
	return testAppYAML("ivan/my-api:1.0.0", 1) + `database:
  postgres:
    instances: 3
    image: ghcr.io/cloudnative-pg/postgresql:17.10@sha256:` + strings.Repeat("a", 64) + `
    database: money_manager
    owner: money_manager
    connectionMode: managed
    storage:
      size: 1Gi
      storageClass: local-path
    synchronous:
      replicas: 1
      dataDurability: required
    retentionPolicy: retain
`
}

func testPostgresAppYAMLForCutover(connectionMode string) string {
	secretNames := "  - JWT_SECRET\n"
	if connectionMode == appconfig.PostgresConnectionModeExternal {
		secretNames = "  - DATABASE_URL\n" + secretNames
	}
	yaml := strings.Replace(
		testPostgresAppYAML(),
		"state:\n",
		"secrets:\n"+secretNames+"state:\n",
		1,
	)
	return strings.Replace(yaml, "connectionMode: managed", "connectionMode: "+connectionMode, 1)
}

func healthyTestDatabaseStatus() domain.DatabaseStatus {
	return domain.DatabaseStatus{
		Present:                     true,
		Phase:                       "Cluster in healthy state",
		DesiredInstances:            3,
		ReadyInstances:              3,
		Primary:                     "my-api-db-1",
		RunningNodes:                []string{"vps", "pi-home", "pi-yasen"},
		RunningInstances:            []string{"my-api-db-1", "my-api-db-2", "my-api-db-3"},
		OwnedByDeployer:             true,
		Image:                       "ghcr.io/cloudnative-pg/postgresql:17.10@sha256:" + strings.Repeat("a", 64),
		BootstrapDatabase:           "money_manager",
		BootstrapOwner:              "money_manager",
		DataChecksumsEnabled:        true,
		StorageSize:                 "1Gi",
		StorageClass:                "local-path",
		SynchronousMethod:           "any",
		SynchronousReplicas:         1,
		DataDurability:              "required",
		FailoverQuorumEnabled:       true,
		AntiAffinityType:            "required",
		TopologyKey:                 "kubernetes.io/hostname",
		Architecture:                "arm64",
		PasswordEncryption:          "scram-sha-256",
		RejectsNonTLS:               true,
		RequiresApplicationSCRAM:    true,
		FailoverQuorumPresent:       true,
		FailoverQuorumMethod:        "any",
		FailoverQuorumStandbyNumber: 1,
		FailoverQuorumPrimary:       "my-api-db-1",
		FailoverQuorumStandbyNames:  []string{"my-api-db-2", "my-api-db-3"},
	}
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
	reconciled            []appconfig.Config
	secretValues          []map[string]string
	secretRevisions       []string
	deleted               []string
	status                string
	desiredReplicas       int32
	availableReplicas     int32
	runningNodes          []string
	databaseStatus        domain.DatabaseStatus
	databaseStatusErr     error
	databaseStatusAppName string
	logLines              []string
	logAppName            string
	logTailLines          int32
	logFollow             bool
	err                   error
}

type cutoverAppRuntime struct {
	reconciled  []appconfig.Config
	failManaged bool
}

func (r *cutoverAppRuntime) Reconcile(
	_ context.Context,
	cfg appconfig.Config,
	_ map[string]string,
	_ string,
) error {
	r.reconciled = append(r.reconciled, cfg)
	if r.failManaged && cfg.Database.Postgres != nil &&
		cfg.Database.Postgres.ConnectionMode == appconfig.PostgresConnectionModeManaged {
		return errors.New("managed cutover blocked")
	}
	return nil
}

func (r *cutoverAppRuntime) Delete(context.Context, string) error { return nil }

func (r *cutoverAppRuntime) Status(context.Context, string) (string, error) {
	return "healthy", nil
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

func (r *recordingAppRuntime) DatabaseStatus(_ context.Context, appName string) (domain.DatabaseStatus, error) {
	r.databaseStatusAppName = appName
	status := r.databaseStatus
	status.RunningNodes = append([]string(nil), status.RunningNodes...)
	status.RunningInstances = append([]string(nil), status.RunningInstances...)
	return status, r.databaseStatusErr
}

func (r *recordingAppRuntime) StreamLogs(_ context.Context, appName string, tailLines int32, follow bool, send func(string) error) error {
	r.logAppName = appName
	r.logTailLines = tailLines
	r.logFollow = follow
	for _, line := range r.logLines {
		if err := send(line); err != nil {
			return err
		}
	}
	return r.err
}

type recordingAppLogStream struct {
	grpc.ServerStream
	ctx   context.Context
	lines []string
}

func (s *recordingAppLogStream) Context() context.Context {
	return s.ctx
}

func (s *recordingAppLogStream) Send(response *deployerv1.GetDeploymentLogsResponse) error {
	s.lines = append(s.lines, response.GetLines()...)
	return nil
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
