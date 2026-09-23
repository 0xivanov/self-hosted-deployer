package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/db/migrations"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestRegistryCredentialRepositoryStoresImmutableMetadata(t *testing.T) {
	ctx := context.Background()
	repo := NewRegistryCredentialRepository(openRepositoryTestDB(t))
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	credential := domain.RegistryCredential{Revision: strings.Repeat("a", 64), AppName: "checkout-api", Registry: "ghcr.io", Ciphertext: "encrypted-one", CreatedAt: now}
	if err := repo.Create(ctx, credential); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, domain.RegistryCredential{Revision: credential.Revision, AppName: credential.AppName, Registry: credential.Registry, Ciphertext: "encrypted-two", CreatedAt: now.Add(time.Hour)}); !errors.Is(err, ErrRegistryCredentialExists) {
		t.Fatalf("duplicate credential was mutable: %v", err)
	}
	found, err := repo.Find(ctx, credential.AppName, credential.Revision)
	if err != nil || found.Ciphertext != credential.Ciphertext || !found.CreatedAt.Equal(now) {
		t.Fatalf("stored credential %#v: %v", found, err)
	}
	list, err := repo.ListByApp(ctx, credential.AppName)
	if err != nil || len(list) != 1 || list[0].Ciphertext != "" || list[0].Registry != credential.Registry {
		t.Fatalf("metadata list %#v: %v", list, err)
	}
}

func TestRegistryCredentialRepositoryValidationAndLimit(t *testing.T) {
	ctx := context.Background()
	repo := NewRegistryCredentialRepository(openRepositoryTestDB(t))
	now := time.Now().UTC()
	base := domain.RegistryCredential{Revision: strings.Repeat("0", 64), AppName: "valid-app", Registry: "docker.io", Ciphertext: "cipher", CreatedAt: now}
	for _, invalid := range []domain.RegistryCredential{
		{Revision: "bad", AppName: base.AppName, Registry: base.Registry, Ciphertext: base.Ciphertext, CreatedAt: now},
		{Revision: base.Revision, AppName: "Bad", Registry: base.Registry, Ciphertext: base.Ciphertext, CreatedAt: now},
		{Revision: base.Revision, AppName: base.AppName, Registry: "example.com", Ciphertext: base.Ciphertext, CreatedAt: now},
		{Revision: base.Revision, AppName: base.AppName, Registry: base.Registry, Ciphertext: strings.Repeat("x", 32769), CreatedAt: now},
	} {
		if err := repo.Create(ctx, invalid); !errors.Is(err, ErrInvalidRegistryCredential) {
			t.Fatalf("invalid credential accepted: %v", err)
		}
	}
	for i := 0; i < maxRegistryCredentialRevisions; i++ {
		revision := strings.Repeat("0", 62) + hexDigit(i/16) + hexDigit(i%16)
		if err := repo.Create(ctx, domain.RegistryCredential{Revision: revision, AppName: base.AppName, Registry: base.Registry, Ciphertext: base.Ciphertext, CreatedAt: now}); err != nil {
			t.Fatalf("create revision %d: %v", i, err)
		}
	}
	if err := repo.Create(ctx, domain.RegistryCredential{Revision: strings.Repeat("f", 64), AppName: base.AppName, Registry: base.Registry, Ciphertext: base.Ciphertext, CreatedAt: now}); !errors.Is(err, ErrRegistryCredentialLimit) {
		t.Fatalf("expected revision limit, got %v", err)
	}
	if err := repo.Create(ctx, base); !errors.Is(err, ErrRegistryCredentialExists) {
		t.Fatalf("duplicate at cap: %v", err)
	}
	other := base
	other.AppName = "other-app"
	if err := repo.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteByApp(ctx, "missing-app"); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteByApp(ctx, base.AppName); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteByApp(ctx, base.AppName); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Find(ctx, other.AppName, other.Revision); err != nil {
		t.Fatalf("sibling credential deleted: %v", err)
	}
}

func hexDigit(n int) string {
	return string("0123456789abcdef"[n])
}

func TestRegistryCredentialMigrationPreservesExistingData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migration.db")
	sqlDB, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	goose.SetBaseFS(migrations.FS)
	if err = goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpToContext(ctx, sqlDB, ".", 6); err != nil {
		t.Fatal(err)
	}
	created := nowString()
	if _, err = sqlDB.ExecContext(ctx, `INSERT INTO apps (id, name, image, desired_state_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, "app-existing", "existing-app", "example/app:1", "{}", created, created); err != nil {
		t.Fatal(err)
	}
	if _, err = sqlDB.ExecContext(ctx, `INSERT INTO app_secrets(app_id,name,ciphertext,created_at,updated_at) VALUES(?,?,?,?,?)`, "app-existing", "EXISTING", "existing-ciphertext", created, created); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpContext(ctx, sqlDB, "."); err != nil {
		t.Fatal(err)
	}
	database := New(sqlDB)
	secret, err := NewSecretRepository(database).Find(ctx, "app-existing", "EXISTING")
	if err != nil || secret.Ciphertext != "existing-ciphertext" {
		t.Fatal("existing application secret changed")
	}

	app, err := NewAppRepository(database).FindActiveByName(ctx, "existing-app")
	if err != nil || app.ID != "app-existing" {
		t.Fatalf("existing data after migration %#v: %v", app, err)
	}
	if err = NewRegistryCredentialRepository(database).Create(ctx, domain.RegistryCredential{Revision: strings.Repeat("a", 64), AppName: "existing-app", Registry: "ghcr.io", Ciphertext: "cipher", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func nowString() string { return time.Now().UTC().Format(time.RFC3339Nano) }
