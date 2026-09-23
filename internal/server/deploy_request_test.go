package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func newDeployRequestTestService(database *db.Db, runtime AppRuntime, requests DeploymentRequestRepository) AppService {
	return NewAppService(AppServiceConfig{
		Apps:               db.NewAppRepository(database),
		Deployments:        db.NewDeploymentRepository(database),
		Routes:             db.NewRouteRepository(database),
		Runtime:            runtime,
		DeploymentRequests: requests,
	})
}

func reopenDeployRequestDB(t *testing.T) (*db.Db, string, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployer.db")
	database, err := db.Open(context.Background(), "file:"+path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return database, path, func() { _ = database.Close() }
}

func TestDeployRequestResultSurvivesServiceReopen(t *testing.T) {
	database, path, closeDB := reopenDeployRequestDB(t)
	defer closeDB()
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	requestID := strings.Repeat("a", 64)
	runtime := &recordingAppRuntime{status: "healthy", desiredReplicas: 2, availableReplicas: 2}
	requests := db.NewDeploymentRequestRepository(database)
	service := newDeployRequestTestService(database, runtime, requests)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{RequestId: requestID, DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 2)}); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}
	database, err := db.Open(context.Background(), "file:"+path)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer database.Close()
	fresh := newDeployRequestTestService(database, &recordingAppRuntime{}, db.NewDeploymentRequestRepository(database))
	metadata, err := fresh.GetDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "my-api", RequestId: requestID})
	if err != nil || metadata.GetState() != "applied" || metadata.GetResult() == nil {
		t.Fatalf("stored result: metadata=%#v err=%v", metadata, err)
	}
}

type withdrawalRequestRuntime struct {
	recordingAppRuntime
	failReconcile bool
}

func (r *withdrawalRequestRuntime) Reconcile(ctx context.Context, cfg appconfig.Config, values map[string]string, revision string) error {
	r.reconciled = append(r.reconciled, cfg)
	r.secretValues = append(r.secretValues, values)
	r.secretRevisions = append(r.secretRevisions, revision)
	if r.failReconcile {
		return apierrors.NewBadRequest("apply rejected")
	}
	return nil
}

func (r *withdrawalRequestRuntime) Delete(_ context.Context, appName string) error {
	r.deleted = append(r.deleted, appName)
	return nil
}

func TestWithdrawnDeployRequestStoresProof(t *testing.T) {
	database := openTestDB(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	requestID := strings.Repeat("b", 64)
	runtime := &withdrawalRequestRuntime{failReconcile: true}
	service := newDeployRequestTestService(database, runtime, db.NewDeploymentRequestRepository(database))
	response, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{RequestId: requestID, ReportWithdrawal: true, DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1)})
	if err != nil || response == nil || !response.GetWithdrawalConfirmed() {
		t.Fatalf("withdrawal: response=%#v err=%v", response, err)
	}
	metadata, err := service.GetDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "my-api", RequestId: requestID})
	if err != nil || metadata.GetState() != "withdrawn" || metadata.GetResult() == nil || !metadata.GetResult().GetWithdrawalConfirmed() {
		t.Fatalf("withdrawal record: metadata=%#v err=%v", metadata, err)
	}
}

func TestDeployRequestIdentityCannotBeReusedForDifferentConfig(t *testing.T) {
	database := openTestDB(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	requestID := strings.Repeat("c", 64)
	runtime := &recordingAppRuntime{status: "healthy", desiredReplicas: 1, availableReplicas: 1}
	service := newDeployRequestTestService(database, runtime, db.NewDeploymentRequestRepository(database))
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{RequestId: requestID, DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1)}); err != nil {
		t.Fatalf("first deploy: %v", err)
	}
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{RequestId: requestID, DeployerYaml: testAppYAML("ivan/my-api:2.0.0", 1)}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected immutable request identity rejection, got %v", err)
	}
	if len(runtime.reconciled) != 1 {
		t.Fatalf("reused request executed again: %d reconciles", len(runtime.reconciled))
	}
}

func TestPendingDeployRequestBlocksReplayAndLegacyMutation(t *testing.T) {
	database, path, closeDB := reopenDeployRequestDB(t)
	defer closeDB()
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	requestID := strings.Repeat("d", 64)
	runtime := &recordingAppRuntime{status: "healthy", desiredReplicas: 1, availableReplicas: 1}
	requests := db.NewDeploymentRequestRepository(database)
	service := newDeployRequestTestService(database, runtime, requests)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{RequestId: strings.Repeat("e", 64), DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1)}); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	cfg, err := appconfig.Parse([]byte(testAppYAML("ivan/my-api:1.0.0", 1)))
	if err != nil {
		t.Fatalf("parse pending config: %v", err)
	}
	requestedState, err := cfg.JSON()
	if err != nil {
		t.Fatalf("encode pending config: %v", err)
	}
	if _, _, err := requests.Begin(ctx, domain.DeployRequest{AppName: "my-api", RequestID: requestID, State: "pending", RequestedState: string(requestedState), CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("seed pending request: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close before recovery: %v", err)
	}
	database, err = db.Open(context.Background(), "file:"+path)
	if err != nil {
		t.Fatalf("reopen before recovery: %v", err)
	}
	defer database.Close()
	runtime = &recordingAppRuntime{status: "healthy", desiredReplicas: 1, availableReplicas: 1}
	requests = db.NewDeploymentRequestRepository(database)
	service = newDeployRequestTestService(database, runtime, requests)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{RequestId: requestID, DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1)}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected pending replay rejection, got %v", err)
	}
	count := len(runtime.reconciled)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.1", 1)}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected pending legacy deploy rejection, got %v", err)
	}
	if _, err := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "my-api"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected pending delete rejection, got %v", err)
	}
	if len(runtime.reconciled) != count || len(runtime.deleted) != 0 {
		t.Fatalf("pending request allowed mutation: reconciles=%d deletes=%d", len(runtime.reconciled), len(runtime.deleted))
	}
}

type completionFailureRequests struct {
	*db.DeploymentRequestRepository
	completeCalls int
}

func (r *completionFailureRequests) Complete(context.Context, string, string, string, string, time.Time) error {
	r.completeCalls++
	return errors.New("journal unavailable")
}

func TestDeployRequestCompletionFailureLeavesPendingWithoutResult(t *testing.T) {
	database := openTestDB(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	requestID := strings.Repeat("f", 64)
	base := db.NewDeploymentRequestRepository(database)
	requests := &completionFailureRequests{DeploymentRequestRepository: base}
	service := newDeployRequestTestService(database, &recordingAppRuntime{status: "healthy", desiredReplicas: 1, availableReplicas: 1}, requests)
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{RequestId: requestID, DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1)}); status.Code(err) != codes.Internal {
		t.Fatalf("expected journal completion failure, got %v", err)
	}
	record, err := base.Find(ctx, "my-api", requestID)
	if err != nil || record.State != "pending" || record.ResponseJSON != "" || requests.completeCalls != 1 {
		t.Fatalf("completion failure changed journal: record=%#v calls=%d err=%v", record, requests.completeCalls, err)
	}
}
