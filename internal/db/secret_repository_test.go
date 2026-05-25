package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestSecretRepositorySetListAndDeleteEncryptedValue(t *testing.T) {
	ctx := context.Background()
	repo := NewSecretRepository(openRepositoryTestDB(t))
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)

	if err := repo.Set(ctx, domain.Secret{
		AppID:      "app-1",
		Name:       "DATABASE_URL",
		Ciphertext: "encrypted-first",
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	if err := repo.Set(ctx, domain.Secret{
		AppID:      "app-1",
		Name:       "DATABASE_URL",
		Ciphertext: "encrypted-updated",
		CreatedAt:  now,
		UpdatedAt:  now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("update secret: %v", err)
	}

	secret, err := repo.Find(ctx, "app-1", "DATABASE_URL")
	if err != nil || secret.Ciphertext != "encrypted-updated" {
		t.Fatalf("unexpected stored secret %#v: %v", secret, err)
	}
	names, err := repo.ListNamesByApp(ctx, "app-1")
	if err != nil || len(names) != 1 || names[0] != "DATABASE_URL" {
		t.Fatalf("unexpected secret names %#v: %v", names, err)
	}
	if err := repo.Delete(ctx, "app-1", "DATABASE_URL"); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	if _, err := repo.Find(ctx, "app-1", "DATABASE_URL"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted secret to be absent, got %v", err)
	}
}
