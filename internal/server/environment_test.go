package server

import (
	"context"
	"errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEnvironmentBundleCanStageBeforeAppAndRetryImmutably(t *testing.T) {
	database := openTestDB(t)
	bundles := db.NewEnvironmentBundleRepository(database)
	cipher := newTestSecretCipher(t)
	service := NewEnvironmentBundleService(bundles, cipher)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	revision := strings.Repeat("a", 64)
	values := map[string]string{"EMPTY": "", "DATABASE_URL": "postgres://private"}
	first, err := service.CreateEnvironmentBundle(ctx, &deployerv1.CreateEnvironmentBundleRequest{AppName: "staged-app", Revision: revision, Values: values})
	if err != nil || first.GetBundle().GetAppName() != "staged-app" {
		t.Fatalf("stage environment bundle: %#v %v", first, err)
	}
	second, err := service.CreateEnvironmentBundle(ctx, &deployerv1.CreateEnvironmentBundleRequest{AppName: "staged-app", Revision: revision, Values: values})
	if err != nil || second.GetBundle().GetRevision() != revision {
		t.Fatalf("idempotent retry: %#v %v", second, err)
	}
	_, err = service.CreateEnvironmentBundle(ctx, &deployerv1.CreateEnvironmentBundleRequest{AppName: "staged-app", Revision: revision, Values: map[string]string{"OTHER": "changed"}})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("accepted changed immutable revision: %v", err)
	}
	emptyRevision := strings.Repeat("b", 64)
	empty, err := service.CreateEnvironmentBundle(ctx, &deployerv1.CreateEnvironmentBundleRequest{AppName: "staged-app", Revision: emptyRevision, Values: map[string]string{}})
	if err != nil || len(empty.GetBundle().GetNames()) != 0 {
		t.Fatalf("empty environment bundle was not accepted: %#v %v", empty, err)
	}
}

func TestDeployEnvironmentMissingBundleFailsBeforeAppAndDeploymentWrites(t *testing.T) {
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	deployments := db.NewDeploymentRepository(database)
	routes := db.NewRouteRepository(database)
	service := NewAppService(AppServiceConfig{Apps: apps, Deployments: deployments, Routes: routes, EnvironmentBundles: db.NewEnvironmentBundleRepository(database), Cipher: newTestSecretCipher(t)})
	yaml := strings.Replace(testAppYAML("ivan/my-api:1.0.0", 1), "name: my-api", "name: my-api\nenvironmentRevision: "+strings.Repeat("c", 64), 1)
	_, err := service.DeployApp(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.DeployAppRequest{DeployerYaml: yaml})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing bundle error: %v", err)
	}
	if _, err := apps.FindByName(context.Background(), "my-api"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("app was written before bundle resolution: %v", err)
	}
	if deploymentsForTest, err := deployments.ListByApp(context.Background(), "missing"); err != nil || len(deploymentsForTest) != 0 {
		t.Fatalf("unexpected deployment state: %#v %v", deploymentsForTest, err)
	}
}

func TestAppServiceSerializesMutationsWithContextCancellation(t *testing.T) {
	service := NewAppService(AppServiceConfig{})
	release, err := service.acquireOperation(context.Background(), "my-api")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.acquireOperation(ctx, "my-api"); err == nil {
		t.Fatal("second mutation was not context-cancelable")
	}
}

type failingEnvironmentRuntime struct{ deleted bool }

func (r *failingEnvironmentRuntime) Reconcile(context.Context, appconfig.Config, map[string]string, string) error {
	return apierrors.NewBadRequest("apply failed")
}
func (r *failingEnvironmentRuntime) Delete(context.Context, string) error {
	r.deleted = true
	return nil
}
func (r *failingEnvironmentRuntime) Status(context.Context, string) (string, error) {
	return "failed", nil
}

func TestDeployReportsConfirmedInitialWithdrawal(t *testing.T) {
	database := openTestDB(t)
	runtime := &failingEnvironmentRuntime{}
	service := NewAppService(AppServiceConfig{Apps: db.NewAppRepository(database), Deployments: db.NewDeploymentRepository(database), Routes: db.NewRouteRepository(database), Runtime: runtime})
	response, err := service.DeployApp(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.DeployAppRequest{DeployerYaml: testAppYAML("ivan/my-api:1.0.0", 1), ReportWithdrawal: true})
	if err != nil {
		t.Fatalf("withdrawal response: %v", err)
	}
	if !response.GetWithdrawalConfirmed() || response.GetDeployment().GetStatus() != deploymentStatusFailed || !runtime.deleted || response.GetRequestedState() == "" {
		t.Fatalf("unexpected withdrawal response: %#v", response)
	}
}

func TestEnvironmentBundleRejectsEmptyValueKeyChangesAndForeignCiphertext(t *testing.T) {
	database := openTestDB(t)
	bundles := db.NewEnvironmentBundleRepository(database)
	cipher := newTestSecretCipher(t)
	service := NewEnvironmentBundleService(bundles, cipher)
	ctx := WithCaller(t.Context(), Caller{Kind: CallerAdmin})
	revision := strings.Repeat("e", 64)
	req := &deployerv1.CreateEnvironmentBundleRequest{AppName: "my-api", Revision: revision, Values: map[string]string{"A": ""}}
	if _, err := service.CreateEnvironmentBundle(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.Values = map[string]string{"B": ""}
	if _, err := service.CreateEnvironmentBundle(ctx, req); status.Code(err) != codes.AlreadyExists {
		t.Fatal("empty values hid immutable key change")
	}
	row, err := bundles.Find(ctx, "my-api", revision)
	if err != nil {
		t.Fatal(err)
	}
	row.AppName = "other-app"
	if err := bundles.Create(ctx, row); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveEnvironmentBundle(ctx, bundles, cipher, "other-app", revision); err == nil {
		t.Fatal("foreign ciphertext resolved")
	}
	if _, err := service.CreateEnvironmentBundle(WithCaller(t.Context(), Caller{Kind: CallerAgent}), req); status.Code(err) != codes.PermissionDenied {
		t.Fatal("agent created environment")
	}
}
