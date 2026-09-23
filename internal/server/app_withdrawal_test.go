package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeployWithdrawalRestoresOriginalEnvironmentAndReturnsProof(t *testing.T) {
	database := openTestDB(t)
	runtime := &environmentRuntime{recordingAppRuntime: recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}}
	firstRevision := strings.Repeat("e", 64)
	secondRevision := strings.Repeat("f", 64)
	stageEnvironmentBundle(t, database, "my-api", firstRevision, map[string]string{"TOKEN": "original"})
	stageEnvironmentBundle(t, database, "my-api", secondRevision, map[string]string{"TOKEN": "latest"})
	service := newEnvironmentAppService(t, database, runtime)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(firstRevision, "ivan/my-api:1.0.0")}); err != nil {
		t.Fatal(err)
	}
	runtime.failRevision = secondRevision
	response, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(secondRevision, "ivan/my-api:1.0.1"), ReportWithdrawal: true})
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || !response.GetWithdrawalConfirmed() || response.GetDeployment().GetStatus() != deploymentStatusFailed || response.GetRequestedState() == "" {
		t.Fatalf("withdrawal proof: %#v", response)
	}
	if len(runtime.secretValues) != 3 || runtime.secretValues[2]["TOKEN"] != "original" {
		t.Fatalf("withdrawal did not restore original environment: %#v", runtime.secretValues)
	}
	apps := db.NewAppRepository(database)
	app, err := apps.FindByName(ctx, "my-api")
	if err != nil {
		t.Fatal(err)
	}
	if app.Image != "ivan/my-api:1.0.0" || !strings.Contains(app.DesiredStateJSON, firstRevision) || strings.Contains(app.DesiredStateJSON, secondRevision) {
		t.Fatalf("withdrawal did not restore original app state: %#v", app)
	}
}

func TestInitialWithdrawalCleanupFailureReturnsNoProof(t *testing.T) {
	database := openTestDB(t)
	runtime := &withdrawalCleanupFailureRuntime{}
	service := NewAppService(AppServiceConfig{Apps: db.NewAppRepository(database), Deployments: db.NewDeploymentRepository(database), Routes: db.NewRouteRepository(database), Runtime: runtime})
	response, err := service.DeployApp(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1), ReportWithdrawal: true})
	if status.Code(err) != codes.Internal || response != nil {
		t.Fatalf("cleanup failure produced withdrawal proof: response=%#v err=%v", response, err)
	}
}

type withdrawalFailDeploymentRepository struct {
	DeploymentRepository
}

func (r withdrawalFailDeploymentRepository) UpdateStatus(ctx context.Context, id, state, reason string, updated time.Time) error {
	if state == deploymentStatusFailed {
		return errors.New("failed status persistence")
	}
	return r.DeploymentRepository.UpdateStatus(ctx, id, state, reason, updated)
}

func TestWithdrawalStatusPersistenceFailureReturnsNoProof(t *testing.T) {
	database := openTestDB(t)
	runtime := &environmentRuntime{recordingAppRuntime: recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}}
	deployments := withdrawalFailDeploymentRepository{DeploymentRepository: db.NewDeploymentRepository(database)}
	service := NewAppService(AppServiceConfig{
		Apps:               db.NewAppRepository(database),
		Deployments:        deployments,
		Routes:             db.NewRouteRepository(database),
		Runtime:            runtime,
		EnvironmentBundles: db.NewEnvironmentBundleRepository(database),
		Cipher:             newTestSecretCipher(t),
	})
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	firstRevision := strings.Repeat("1", 64)
	secondRevision := strings.Repeat("2", 64)
	stageEnvironmentBundle(t, database, "my-api", firstRevision, map[string]string{"TOKEN": "original"})
	stageEnvironmentBundle(t, database, "my-api", secondRevision, map[string]string{"TOKEN": "latest"})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(firstRevision, "ivan/my-api:1.0.0")}); err != nil {
		t.Fatal(err)
	}
	runtime.failRevision = secondRevision
	response, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(secondRevision, "ivan/my-api:1.0.1"), ReportWithdrawal: true})
	if status.Code(err) != codes.Internal || response != nil {
		t.Fatalf("status persistence failure produced proof: response=%#v err=%v", response, err)
	}
}

func TestFailedDeploymentWithoutWithdrawalReportRemainsLegacyError(t *testing.T) {
	database := openTestDB(t)
	runtime := &environmentRuntime{recordingAppRuntime: recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}}
	service := newEnvironmentAppService(t, database, runtime)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	firstRevision := strings.Repeat("3", 64)
	secondRevision := strings.Repeat("4", 64)
	stageEnvironmentBundle(t, database, "my-api", firstRevision, map[string]string{"TOKEN": "original"})
	stageEnvironmentBundle(t, database, "my-api", secondRevision, map[string]string{"TOKEN": "latest"})
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(firstRevision, "ivan/my-api:1.0.0")}); err != nil {
		t.Fatal(err)
	}
	runtime.failRevision = secondRevision
	response, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: environmentAppYAML(secondRevision, "ivan/my-api:1.0.1")})
	if status.Code(err) != codes.Internal || response != nil {
		t.Fatalf("legacy failure returned structured response: response=%#v err=%v", response, err)
	}
}

var _ DeploymentRepository = withdrawalFailDeploymentRepository{}

type withdrawalCleanupFailureRuntime struct{ failingEnvironmentRuntime }

func (r *withdrawalCleanupFailureRuntime) Delete(context.Context, string) error {
	return errors.New("cleanup failed")
}

func TestAmbiguousRuntimeFailureNeverConfirmsWithdrawal(t *testing.T) {
	database := openTestDB(t)
	runtime := &environmentRuntime{recordingAppRuntime: recordingAppRuntime{err: context.DeadlineExceeded}}
	service := newEnvironmentAppService(t, database, runtime)
	response, err := service.DeployApp(WithCaller(t.Context(), Caller{Kind: CallerAdmin}), &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1), ReportWithdrawal: true})
	if err == nil || response != nil {
		t.Fatal("timeout became withdrawal proof")
	}
}
