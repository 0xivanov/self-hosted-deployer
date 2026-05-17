package apperrors

import (
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/repository"
)

func TestInvalidArgumentUsesCanonicalCode(t *testing.T) {
	if got := CodeOf(InvalidArgument("bad input")); got != CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument, got %s", got)
	}
}

func TestFromRepositoryErrorMapsNotFound(t *testing.T) {
	if got := CodeOf(FromRepositoryError(repository.ErrNotFound, "node")); got != CodeNotFound {
		t.Fatalf("expected NotFound, got %s", got)
	}
}
