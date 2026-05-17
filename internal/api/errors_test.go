package api

import "testing"

func TestInvalidArgumentUsesCanonicalCode(t *testing.T) {
	if got := CodeOf(InvalidArgument("bad input")); got != CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument, got %s", got)
	}
}

func TestFromStoreErrorMapsNotFound(t *testing.T) {
	if got := CodeOf(FromStoreError(ErrNotFound, "node")); got != CodeNotFound {
		t.Fatalf("expected NotFound, got %s", got)
	}
}
