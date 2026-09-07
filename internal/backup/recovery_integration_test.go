//go:build integration

package backup_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/backup"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
)

// This drill uses real platform repositories and keys, but only synthetic data.
// It never starts a control plane, agent, reconciler, or network connection.
func TestPlatformRecoveryWithoutOriginalHost(t *testing.T) {
	ctx := context.Background()
	failedHost := filepath.Join(t.TempDir(), "failed-host")
	if err := os.Mkdir(failedHost, 0700); err != nil {
		t.Fatal(err)
	}
	recoveryMedia := t.TempDir()
	freshHost := t.TempDir()
	sourcePath := filepath.Join(failedHost, "deployer.db")
	sourceURL := url.URL{Scheme: "file", Path: sourcePath, RawQuery: "_pragma=journal_mode(WAL)"}
	source, err := db.Open(ctx, sourceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	backupKey := make([]byte, 32)
	secretKey := make([]byte, 32)
	tokenKey := make([]byte, 32)
	for _, key := range [][]byte{backupKey, secretKey, tokenKey} {
		if _, err := rand.Read(key); err != nil {
			t.Fatal(err)
		}
	}
	backupKeyPath := filepath.Join(recoveryMedia, "backup.key")
	secretKeyPath := filepath.Join(recoveryMedia, "secret.key")
	tokenKeyPath := filepath.Join(recoveryMedia, "token.key")
	for path, key := range map[string][]byte{backupKeyPath: backupKey, secretKeyPath: secretKey, tokenKeyPath: tokenKey} {
		if err := os.WriteFile(path, key, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cipher, err := security.NewSecretCipher(secretKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt("synthetic-secret-before-snapshot")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := appconfig.Parse([]byte(`name: recovery-api
image: example/recovery:v1
service:
  port: 8080
  health: {path: /health}
deploy: {replicas: 1}
hosting:
  version: v1
  maxReplicas: 1
  resources:
    requests: {cpu: 100m, memory: 32Mi, ephemeralStorage: 32Mi}
    limits: {cpu: 200m, memory: 64Mi, ephemeralStorage: 64Mi}
`))
	if err != nil {
		t.Fatal(err)
	}
	desired, err := cfg.JSON()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	app := domain.App{ID: "app-recovery", Name: cfg.Name, Image: cfg.Image, DesiredStateJSON: desired, CreatedAt: now, UpdatedAt: now}
	if err := db.NewAppRepository(source).Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	secret := domain.Secret{AppID: app.ID, Name: "RECOVERY_SECRET", Ciphertext: ciphertext, CreatedAt: now, UpdatedAt: now}
	if err := db.NewSecretRepository(source).Set(ctx, secret); err != nil {
		t.Fatal(err)
	}
	tokenHash, err := security.HashToken(tokenKey, "synthetic-token")
	if err != nil {
		t.Fatal(err)
	}
	token := domain.AdminToken{TokenHash: tokenHash, Name: "recovery-operator", CreatedAt: now, RevokedAt: &now}
	if err := db.NewAdminTokenRepository(source).Create(ctx, token); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(recoveryMedia, "platform.backup")
	if err := backup.Create(ctx, backup.CreateOptions{DatabasePath: sourcePath, OutputPath: artifact, KeyFile: backupKeyPath}); err != nil {
		t.Fatal(err)
	}
	// A later committed update must not leak into the earlier recovery point.
	app.Image = "example/recovery:after-backup"
	if err := db.NewAppRepository(source).Update(ctx, app); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(failedHost); err != nil {
		t.Fatal(err)
	}
	// Drop in-memory key material. Recover only from the separate recovery media.
	clear(backupKey)
	clear(secretKey)
	clear(tokenKey)
	cipher = nil
	restoredPath := filepath.Join(freshHost, "deployer.db")
	started := time.Now()
	if err := backup.Restore(ctx, backup.RestoreOptions{InputPath: artifact, OutputPath: restoredPath, KeyFile: backupKeyPath}); err != nil {
		t.Fatal(err)
	}
	restoredURL := url.URL{Scheme: "file", Path: restoredPath}
	restored, err := db.Open(ctx, restoredURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := db.NewAppRepository(restored).FindActiveByName(ctx, cfg.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != cfg.Image || got.DesiredStateJSON != desired {
		t.Fatal("recovery point or hosting profile changed")
	}
	parsed, err := appconfig.FromJSON(got.DesiredStateJSON)
	if err != nil || parsed.Hosting == nil {
		t.Fatalf("restored hosting state: %v", err)
	}
	gotSecret, err := db.NewSecretRepository(restored).Find(ctx, app.ID, secret.Name)
	if err != nil {
		t.Fatal(err)
	}
	recoveredKey, err := os.ReadFile(secretKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	recoveredCipher, err := security.NewSecretCipher(recoveredKey)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := recoveredCipher.Decrypt(gotSecret.Ciphertext)
	if err != nil || plain != "synthetic-secret-before-snapshot" {
		t.Fatal("restored application secret cannot be decrypted")
	}
	wrongCipher, err := security.NewSecretCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongCipher.Decrypt(gotSecret.Ciphertext); err == nil {
		t.Fatal("unrelated key decrypted restored secret")
	}
	recoveredTokenKey, err := os.ReadFile(tokenKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	recoveredHash, err := security.HashToken(recoveredTokenKey, "synthetic-token")
	if err != nil {
		t.Fatal(err)
	}
	gotToken, err := db.NewAdminTokenRepository(restored).FindByHash(ctx, recoveredHash)
	if err != nil || gotToken.RevokedAt == nil {
		t.Fatalf("token revocation lost: %v", err)
	}
	gotSecret.Ciphertext, err = recoveredCipher.Encrypt("synthetic-after-restore")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.NewSecretRepository(restored).Set(ctx, gotSecret); err != nil {
		t.Fatal(err)
	}
	after, err := db.NewSecretRepository(restored).Find(ctx, app.ID, secret.Name)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := recoveredCipher.Decrypt(after.Ciphertext); err != nil || value != "synthetic-after-restore" {
		t.Fatal("restored repository is not writable")
	}
	t.Logf("synthetic platform database recovery and validation completed in %s; not a full-environment RTO", time.Since(started))
}
