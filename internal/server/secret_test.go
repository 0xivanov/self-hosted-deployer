package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSecretServiceSetListAndDeleteStoresOnlyCiphertext(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	secrets := db.NewSecretRepository(database)
	cipher := newTestSecretCipher(t)
	eventRecorder := &recordingEventRecorder{}
	createSecretTestApp(t, apps, nil)
	service := NewSecretService(SecretServiceConfig{Apps: apps, Secrets: secrets, Cipher: cipher, Events: eventRecorder})

	if _, err := service.SetSecret(ctx, &deployerv1.SetSecretRequest{
		AppName: "my-api",
		Name:    "DATABASE_URL",
		Value:   "postgres://plain-value",
	}); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	stored, err := secrets.Find(ctx, "app-1", "DATABASE_URL")
	if err != nil {
		t.Fatalf("find stored secret: %v", err)
	}
	if stored.Ciphertext == "postgres://plain-value" || strings.Contains(stored.Ciphertext, "plain-value") {
		t.Fatalf("plaintext was stored in ciphertext field: %q", stored.Ciphertext)
	}
	decrypted, err := cipher.Decrypt(stored.Ciphertext)
	if err != nil || decrypted != "postgres://plain-value" {
		t.Fatalf("decrypt stored secret got %q: %v", decrypted, err)
	}
	if _, err := service.SetSecret(ctx, &deployerv1.SetSecretRequest{
		AppName: "my-api",
		Name:    "DATABASE_URL",
		Value:   "postgres://updated-value",
	}); err != nil {
		t.Fatalf("update secret: %v", err)
	}
	listed, err := service.ListSecrets(ctx, &deployerv1.ListSecretsRequest{AppName: "my-api"})
	if err != nil || len(listed.GetNames()) != 1 || listed.GetNames()[0] != "DATABASE_URL" {
		t.Fatalf("unexpected secret names %#v: %v", listed, err)
	}
	if _, err := service.DeleteSecret(ctx, &deployerv1.DeleteSecretRequest{AppName: "my-api", Name: "DATABASE_URL"}); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	if _, err := secrets.Find(ctx, "app-1", "DATABASE_URL"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected deleted secret to be absent, got %v", err)
	}
	for _, eventType := range []domain.EventType{domain.EventTypeSecretCreated, domain.EventTypeSecretUpdated, domain.EventTypeSecretDeleted} {
		if !eventRecorder.hasType(eventType) {
			t.Fatalf("expected secret event %s, got %#v", eventType, eventRecorder.events)
		}
	}
	for _, event := range eventRecorder.events {
		if strings.Contains(event.MetadataJSON, "plain-value") || strings.Contains(event.MetadataJSON, "updated-value") {
			t.Fatalf("secret metadata contained value: %q", event.MetadataJSON)
		}
	}
}

func TestSecretServiceSetsReferencedSecretsIndependentlyThenReconcilesUpdates(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	apps := db.NewAppRepository(database)
	secrets := db.NewSecretRepository(database)
	cipher := newTestSecretCipher(t)
	runtime := &recordingAppRuntime{}
	createSecretTestApp(t, apps, []string{"DATABASE_URL", "JWT_SECRET"})
	service := NewSecretService(SecretServiceConfig{Apps: apps, Secrets: secrets, Cipher: cipher, Runtime: runtime})

	if _, err := service.SetSecret(ctx, &deployerv1.SetSecretRequest{AppName: "my-api", Name: "DATABASE_URL", Value: "first"}); err != nil {
		t.Fatalf("set referenced secret: %v", err)
	}
	if len(runtime.secretValues) != 0 {
		t.Fatalf("expected incomplete secret setup not to reconcile, got %#v", runtime.secretValues)
	}
	if _, err := service.SetSecret(ctx, &deployerv1.SetSecretRequest{AppName: "my-api", Name: "JWT_SECRET", Value: "jwt"}); err != nil {
		t.Fatalf("set final referenced secret: %v", err)
	}
	if _, err := service.SetSecret(ctx, &deployerv1.SetSecretRequest{AppName: "my-api", Name: "DATABASE_URL", Value: "updated"}); err != nil {
		t.Fatalf("update referenced secret: %v", err)
	}
	if len(runtime.secretValues) != 2 || runtime.secretValues[0]["JWT_SECRET"] != "jwt" || runtime.secretValues[1]["DATABASE_URL"] != "updated" {
		t.Fatalf("expected secret update reconciliation, got %#v", runtime.secretValues)
	}
	if len(runtime.secretRevisions) != 2 || runtime.secretRevisions[0] == "" || runtime.secretRevisions[0] == runtime.secretRevisions[1] {
		t.Fatalf("expected changed encrypted-state revision, got %#v", runtime.secretRevisions)
	}
	if _, err := service.DeleteSecret(ctx, &deployerv1.DeleteSecretRequest{AppName: "my-api", Name: "DATABASE_URL"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected referenced secret removal rejection, got %v", err)
	}
}

func TestSecretServiceRejectsCallsWithoutCipherConfiguration(t *testing.T) {
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	service := NewSecretService(SecretServiceConfig{})

	if _, err := service.SetSecret(ctx, &deployerv1.SetSecretRequest{AppName: "my-api", Name: "DATABASE_URL", Value: "value"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected unconfigured secret service rejection, got %v", err)
	}
}

func newTestSecretCipher(t *testing.T) *security.SecretCipher {
	t.Helper()
	cipher, err := security.NewSecretCipher([]byte(strings.Repeat("s", security.SecretKeyBytes)))
	if err != nil {
		t.Fatalf("new secret cipher: %v", err)
	}
	return cipher
}

func createSecretTestApp(t *testing.T, apps *db.AppRepository, names []string) {
	t.Helper()
	cfg := appconfig.Config{
		Name:  "my-api",
		Image: "ivan/my-api:1.0.0",
		Service: appconfig.ServiceConfig{
			Port:   3000,
			Health: appconfig.HealthConfig{Path: "/health"},
		},
		Deploy:  appconfig.DeployConfig{Replicas: 1},
		Secrets: names,
		State:   appconfig.StateConfig{Mode: "stateless"},
	}
	desiredState, err := cfg.JSON()
	if err != nil {
		t.Fatalf("encode app state: %v", err)
	}
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	if err := apps.Create(context.Background(), domain.App{
		ID:               "app-1",
		Name:             cfg.Name,
		Image:            cfg.Image,
		DesiredStateJSON: desiredState,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("create test app: %v", err)
	}
}
