package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteRepositoryPersistsAdminTokens(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "deployer.db")

	repo, err := OpenSQLite(ctx, dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	createdAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	if err := repo.CreateAdminToken(ctx, AdminToken{
		TokenHash: "hash",
		Name:      "bootstrap",
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	reopened, err := OpenSQLite(ctx, dsn)
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

func TestSQLiteRepositoryMapsMissingRowsToNotFound(t *testing.T) {
	repo, err := OpenSQLite(context.Background(), "file:"+filepath.Join(t.TempDir(), "deployer.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer repo.Close()

	_, err = repo.FindAgentTokenByHash(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
