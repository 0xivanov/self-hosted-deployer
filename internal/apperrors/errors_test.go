package apperrors

import (
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
)

func TestInvalidArgumentUsesCanonicalCode(t *testing.T) {
	if got := CodeOf(InvalidArgument("bad input")); got != CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument, got %s", got)
	}
}

func TestFromRepositoryErrorMapsNotFound(t *testing.T) {
	if got := CodeOf(FromRepositoryError(db.ErrNotFound, "node")); got != CodeNotFound {
		t.Fatalf("expected NotFound, got %s", got)
	}
}
