package repository

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryRepositoryReturnsNotFound(t *testing.T) {
	repo := NewMemory()

	_, err := repo.FindAdminTokenByHash(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
