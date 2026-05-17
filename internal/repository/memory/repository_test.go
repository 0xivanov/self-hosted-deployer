package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/repository"
)

func TestMemoryRepositoryReturnsNotFound(t *testing.T) {
	repo := New()

	_, err := repo.FindAdminTokenByHash(context.Background(), "missing")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
