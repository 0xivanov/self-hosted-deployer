package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/repository"
)

func TestSQLiteRepositoryPersistsAdminTokens(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "deployer.db")

	repo, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	createdAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	if err := repo.CreateAdminToken(ctx, repository.AdminToken{
		TokenHash: "hash",
		Name:      "bootstrap",
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	reopened, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer reopened.Close()

	token, err := reopened.FindAdminTokenByHash(ctx, "hash")
	if err != nil {
		t.Fatalf("find admin token: %v", err)
	}
	if token.Name != "bootstrap" {
		t.Fatalf("expected bootstrap token, got %q", token.Name)
	}
	if !token.CreatedAt.Equal(createdAt) {
		t.Fatalf("expected created_at %s, got %s", createdAt, token.CreatedAt)
	}

	usedAt := createdAt.Add(time.Minute)
	if err := reopened.MarkAdminTokenUsed(ctx, "hash", usedAt); err != nil {
		t.Fatalf("mark admin token used: %v", err)
	}
	token, err = reopened.FindAdminTokenByHash(ctx, "hash")
	if err != nil {
		t.Fatalf("find admin token after use: %v", err)
	}
	if token.LastUsedAt == nil || !token.LastUsedAt.Equal(usedAt) {
		t.Fatalf("expected last_used_at %s, got %v", usedAt, token.LastUsedAt)
	}
}

func TestSQLiteRepositoryRecordsMigrationVersion(t *testing.T) {
	repo, err := Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "deployer.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer repo.Close()

	var versionID int64
	var isApplied bool
	err = repo.db.QueryRowContext(context.Background(), `SELECT version_id, is_applied FROM goose_db_version ORDER BY id DESC LIMIT 1`).Scan(&versionID, &isApplied)
	if err != nil {
		t.Fatalf("query migration version: %v", err)
	}
	if versionID != 1 || !isApplied {
		t.Fatalf("expected migration version 1 to be applied, got version=%d applied=%v", versionID, isApplied)
	}
}

func TestSQLiteRepositoryMapsMissingRowsToNotFound(t *testing.T) {
	repo, err := Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "deployer.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer repo.Close()

	_, err = repo.FindAgentTokenByHash(context.Background(), "missing")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
