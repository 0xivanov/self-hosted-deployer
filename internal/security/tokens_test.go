package security

import (
	"strings"
	"testing"
)

func TestNewTokenUsesPrefixAndEntropy(t *testing.T) {
	token, err := NewToken(AdminTokenPrefix)
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if !strings.HasPrefix(token, AdminTokenPrefix+"_") {
		t.Fatalf("expected admin prefix, got %q", token)
	}
	if len(token) < len(AdminTokenPrefix)+1+40 {
		t.Fatalf("token too short: %d", len(token))
	}
}

func TestNewTokenRejectsUnknownPrefix(t *testing.T) {
	if _, err := NewToken("bad"); err == nil {
		t.Fatal("expected error")
	}
}

func TestHashTokenStableWithSameKey(t *testing.T) {
	key := []byte("test-key")
	token := "dep_agent_abc"

	first, err := HashToken(key, token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	second, err := HashToken(key, token)
	if err != nil {
		t.Fatalf("hash token again: %v", err)
	}
	if first != second {
		t.Fatal("expected stable hash")
	}
}

func TestHashTokenDiffersForDifferentTokens(t *testing.T) {
	key := []byte("test-key")
	first, err := HashToken(key, "dep_agent_one")
	if err != nil {
		t.Fatalf("hash first token: %v", err)
	}
	second, err := HashToken(key, "dep_agent_two")
	if err != nil {
		t.Fatalf("hash second token: %v", err)
	}
	if first == second {
		t.Fatal("expected different hashes")
	}
}

func TestRedactTokenDoesNotReturnFullToken(t *testing.T) {
	token, err := NewToken(JoinTokenPrefix)
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	redacted := RedactToken(token)
	if redacted == token {
		t.Fatal("redaction returned full token")
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Fatalf("expected redaction marker, got %q", redacted)
	}
}
