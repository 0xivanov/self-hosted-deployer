package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestJoinTokenRepositoryConsumesActiveTokenOnce(t *testing.T) {
	ctx := context.Background()
	database := openRepositoryTestDB(t)
	repo := NewJoinTokenRepository(database)
	now := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, domain.JoinToken{
		TokenHash:        "hashed-token",
		IntendedNodeName: "pi-kitchen",
		LabelsJSON:       `{"location":"home"}`,
		CreatedAt:        now,
		ExpiresAt:        now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create token: %v", err)
	}

	if _, err := repo.FindByHash(ctx, "raw-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("raw token should not be stored, got %v", err)
	}

	consumed, err := repo.Consume(ctx, "hashed-token", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("consume token: %v", err)
	}
	if consumed.IntendedNodeName != "pi-kitchen" || consumed.UsedAt == nil {
		t.Fatalf("unexpected consumed token: %#v", consumed)
	}

	if _, err := repo.Consume(ctx, "hashed-token", now.Add(2*time.Minute)); !errors.Is(err, ErrJoinTokenUsed) {
		t.Fatalf("expected used token rejection, got %v", err)
	}
}

func TestJoinTokenRepositoryRejectsExpiredToken(t *testing.T) {
	ctx := context.Background()
	database := openRepositoryTestDB(t)
	repo := NewJoinTokenRepository(database)
	now := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, domain.JoinToken{
		TokenHash:        "expired-token",
		IntendedNodeName: "pi-garage",
		LabelsJSON:       "{}",
		CreatedAt:        now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create expired token: %v", err)
	}

	if _, err := repo.Consume(ctx, "expired-token", now); !errors.Is(err, ErrJoinTokenExpired) {
		t.Fatalf("expected expired token rejection, got %v", err)
	}

	stored, err := repo.FindByHash(ctx, "expired-token")
	if err != nil {
		t.Fatalf("find expired token: %v", err)
	}
	if stored.UsedAt != nil {
		t.Fatalf("expired token should not be marked used: %#v", stored)
	}
}
