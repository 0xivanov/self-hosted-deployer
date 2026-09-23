package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func registryRequest() *deployerv1.CreateRegistryCredentialRequest {
	return &deployerv1.CreateRegistryCredentialRequest{AppName: "my-api", Revision: strings.Repeat("a", 64), Registry: "ghcr.io", Username: "test-user", Password: "private-token"}
}
func TestRegistryCredentialsEncryptedImmutableAndScoped(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	repo := db.NewRegistryCredentialRepository(openTestDB(t))
	cipher := newTestSecretCipher(t)
	service := NewRegistryCredentialService(repo, cipher)
	req := registryRequest()
	if _, err := service.CreateRegistryCredential(context.Background(), req); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated=%v", err)
	}
	if _, err := service.CreateRegistryCredential(WithCaller(context.Background(), Caller{Kind: CallerAgent}), req); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("agent=%v", err)
	}
	if _, err := service.CreateRegistryCredential(ctx, req); err != nil {
		t.Fatal(err)
	}
	row, err := repo.Find(ctx, req.AppName, req.Revision)
	if err != nil || strings.Contains(row.Ciphertext, req.Password) {
		t.Fatal("plaintext stored or row missing")
	}
	if _, err := service.CreateRegistryCredential(ctx, req); err != nil {
		t.Fatalf("retry: %v", err)
	}
	req.Password = "rotated-token"
	if _, err := service.CreateRegistryCredential(ctx, req); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("immutable revision changed: %v", err)
	}
	image := "ghcr.io/org/site@sha256:" + strings.Repeat("b", 64)
	credential, err := service.ResolveRegistryCredential(ctx, req.AppName, req.Revision, image)
	if err != nil || credential.Password != "private-token" {
		t.Fatal("original revision lost")
	}
	for _, other := range []struct{ app, image string }{{"another-app", image}, {req.AppName, "docker.io/org/site@sha256:" + strings.Repeat("b", 64)}} {
		if _, err := service.ResolveRegistryCredential(ctx, other.app, req.Revision, other.image); err == nil {
			t.Fatal("incorrect binding allowed")
		}
	}
	list, err := service.ListRegistryCredentials(ctx, &deployerv1.ListRegistryCredentialsRequest{AppName: req.AppName})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "private-token") || strings.Contains(string(raw), req.Username) {
		t.Fatal("list leaked login")
	}
	// Authenticated ciphertext copied to a different row must not become valid.
	copied := row
	copied.AppName = "another-app"
	if err := repo.Create(ctx, copied); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveRegistryCredential(ctx, copied.AppName, copied.Revision, image); err == nil {
		t.Fatal("ciphertext rebound to another app")
	}
	target, _ := json.Marshal(mutationTarget("/deployer.v1.RegistryCredentialService/CreateRegistryCredential", req))
	if !isMutationMethod("/deployer.v1.RegistryCredentialService/CreateRegistryCredential") || strings.Contains(string(target), req.Password) || strings.Contains(string(target), req.Username) {
		t.Fatal("incorrect mutation audit")
	}
}

type privateImageRuntime struct {
	recordingAppRuntime
	credentials  []registryauth.Credential
	failRevision string
}

func (r *privateImageRuntime) ReconcileWithRegistry(ctx context.Context, cfg appconfig.Config, values map[string]string, revision string, c *registryauth.Credential) error {
	if c == nil || c.ValidateFor(cfg.Name, cfg.ImagePullCredential, cfg.Image) != nil {
		return errors.New("invalid credential")
	}
	r.credentials = append(r.credentials, *c)
	if c.Revision == r.failRevision {
		return errors.New("apply failed")
	}
	return r.recordingAppRuntime.Reconcile(ctx, cfg, values, revision)
}
func TestPrivateDeployRotationRollbackAndDeletion(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	repo := db.NewRegistryCredentialRepository(database)
	credentials := NewRegistryCredentialService(repo, newTestSecretCipher(t))
	req := registryRequest()
	if _, err := credentials.CreateRegistryCredential(ctx, req); err != nil {
		t.Fatal(err)
	}
	firstRevision := req.Revision
	req.Revision = strings.Repeat("c", 64)
	req.Password = "new-token"
	if _, err := credentials.CreateRegistryCredential(ctx, req); err != nil {
		t.Fatal(err)
	}
	runtime := &privateImageRuntime{}
	apps := db.NewAppRepository(database)
	service := NewAppService(AppServiceConfig{Apps: apps, Deployments: db.NewDeploymentRepository(database), Routes: db.NewRouteRepository(database), Runtime: runtime, RegistryCredentials: credentials, RegistryCredentialRepository: repo})
	image := "ghcr.io/org/site@sha256:" + strings.Repeat("b", 64)
	yaml := testAppYAML(image, 1) + "imagePullCredential: " + firstRevision + "\n"
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: yaml}); err != nil {
		t.Fatal(err)
	}
	runtime.failRevision = req.Revision
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: strings.ReplaceAll(yaml, firstRevision, req.Revision)}); err == nil {
		t.Fatal("expected failed update")
	}
	if len(runtime.credentials) != 3 || runtime.credentials[2].Revision != firstRevision || runtime.credentials[2].Password != "private-token" {
		t.Fatal("rollback did not retain original credentials")
	}
	app, err := apps.FindByName(ctx, "my-api")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := appconfig.FromJSON(app.DesiredStateJSON)
	if err != nil || cfg.ImagePullCredential != firstRevision || strings.Contains(app.DesiredStateJSON, "private-token") {
		t.Fatal("stored config not restored or includes credential")
	}
	if _, err := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "my-api"}); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListByApp(ctx, "my-api")
	if err != nil || len(rows) != 0 {
		t.Fatal("deleted app retained credentials")
	}
}
func TestPrivateDeploymentWithoutResolverFailsBeforeMutation(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	service := NewAppService(AppServiceConfig{Apps: apps, Runtime: &recordingAppRuntime{}})
	yaml := testAppYAML("ghcr.io/org/site@sha256:"+strings.Repeat("b", 64), 1) + "imagePullCredential: " + strings.Repeat("a", 64) + "\n"
	if _, err := service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: yaml}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unsupported private deployment: %v", err)
	}
	if _, err := apps.FindByName(ctx, "my-api"); !errors.Is(err, db.ErrNotFound) {
		t.Fatal("rejected deployment created app")
	}
}
