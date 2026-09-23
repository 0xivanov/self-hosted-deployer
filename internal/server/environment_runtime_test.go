package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type environmentRuntime struct {
	recordingAppRuntime
	failRevision string
}

func (r *environmentRuntime) Reconcile(ctx context.Context, cfg appconfig.Config, values map[string]string, revision string) error {
	if err := r.recordingAppRuntime.Reconcile(ctx, cfg, values, revision); err != nil {
		return err
	}
	if revision == r.failRevision {
		return errors.New("environment apply failed")
	}
	return nil
}

func stageEnvironmentBundle(t *testing.T, database *db.Db, app, revision string, values map[string]string) {
	t.Helper()
	service := NewEnvironmentBundleService(db.NewEnvironmentBundleRepository(database), newTestSecretCipher(t))
	_, err := service.CreateEnvironmentBundle(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.CreateEnvironmentBundleRequest{AppName: app, Revision: revision, Values: values})
	if err != nil {
		t.Fatal(err)
	}
}

func environmentAppYAML(revision, image string) string {
	return strings.Replace(testAppYAML(image, 2), "name: my-api", "name: my-api\nenvironmentRevision: "+revision, 1)
}

func newEnvironmentAppService(t *testing.T, database *db.Db, runtime AppRuntime) AppService {
	t.Helper()
	return NewAppService(AppServiceConfig{
		Apps:               db.NewAppRepository(database),
		Deployments:        db.NewDeploymentRepository(database),
		Routes:             db.NewRouteRepository(database),
		EnvironmentBundles: db.NewEnvironmentBundleRepository(database),
		Cipher:             newTestSecretCipher(t),
		Runtime:            runtime,
	})
}

func TestDeployEnvironmentInjectsBundleValuesIntoRuntime(t *testing.T) {
	database := openTestDB(t)
	runtime := &environmentRuntime{recordingAppRuntime: recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}}
	revision := strings.Repeat("a", 64)
	values := map[string]string{"DATABASE_URL": "postgres://bundle", "EMPTY": ""}
	stageEnvironmentBundle(t, database, "my-api", revision, values)
	service := newEnvironmentAppService(t, database, runtime)
	if _, err := service.DeployApp(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(revision, "ivan/my-api:1.0.0")}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.secretValues) != 1 || !mapsEqual(runtime.secretValues[0], values) || len(runtime.secretRevisions) != 1 || runtime.secretRevisions[0] != revision {
		t.Fatalf("runtime did not receive bundle: values=%#v revisions=%#v", runtime.secretValues, runtime.secretRevisions)
	}
	if len(runtime.reconciled) != 1 || runtime.reconciled[0].EnvironmentRevision != revision {
		t.Fatalf("runtime config missing environment revision: %#v", runtime.reconciled)
	}
}

func TestFailedEnvironmentUpdateRollsBackUsingOriginalBundle(t *testing.T) {
	database := openTestDB(t)
	runtime := &environmentRuntime{recordingAppRuntime: recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}}
	firstRevision := strings.Repeat("b", 64)
	secondRevision := strings.Repeat("c", 64)
	stageEnvironmentBundle(t, database, "my-api", firstRevision, map[string]string{"TOKEN": "original"})
	stageEnvironmentBundle(t, database, "my-api", secondRevision, map[string]string{"TOKEN": "latest"})
	service := newEnvironmentAppService(t, database, runtime)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(firstRevision, "ivan/my-api:1.0.0")}); err != nil {
		t.Fatal(err)
	}
	runtime.failRevision = secondRevision
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(secondRevision, "ivan/my-api:1.0.1")}); status.Code(err) != codes.Internal {
		t.Fatalf("expected failed update, got %v", err)
	}
	if len(runtime.secretValues) != 3 || runtime.secretValues[1]["TOKEN"] != "latest" || runtime.secretValues[2]["TOKEN"] != "original" {
		t.Fatalf("rollback did not resolve original bundle: %#v", runtime.secretValues)
	}
	apps := db.NewAppRepository(database)
	app, err := apps.FindByName(ctx, "my-api")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := appconfig.FromJSON(app.DesiredStateJSON)
	if err != nil || cfg.EnvironmentRevision != firstRevision || app.Image != "ivan/my-api:1.0.0" {
		t.Fatalf("stored app was not rolled back: %#v %v", cfg, err)
	}
}

func TestDeleteAppCleansEnvironmentBundlesAfterRuntimeDelete(t *testing.T) {
	database := openTestDB(t)
	runtime := &environmentRuntime{recordingAppRuntime: recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}}
	revision := strings.Repeat("d", 64)
	stageEnvironmentBundle(t, database, "my-api", revision, map[string]string{"TOKEN": "delete-me"})
	service := newEnvironmentAppService(t, database, runtime)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(revision, "ivan/my-api:1.0.0")}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "my-api"}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.deleted) != 1 || runtime.deleted[0] != "my-api" {
		t.Fatalf("runtime delete calls: %#v", runtime.deleted)
	}
	if _, err := db.NewEnvironmentBundleRepository(database).Find(ctx, "my-api", revision); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("environment bundle remained after app deletion: %v", err)
	}
}

var _ AppRuntime = (*environmentRuntime)(nil)
