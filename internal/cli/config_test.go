package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadConfigUsesRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deployer", "config.json")
	want := Config{
		ServerURL:  "localhost:7443",
		AdminToken: "dep_admin_test",
		Output:     OutputJSON,
	}

	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("save config: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected config mode 0600, got %o", got)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got != want {
		t.Fatalf("loaded config mismatch: got %#v want %#v", got, want)
	}
}

func TestLoadConfigMissingFileIsActionable(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestContextCredentialReferences(t *testing.T) {
	t.Setenv("DEPLOYER_TEST_CONTEXT_TOKEN", "env-token")
	secretPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(secretPath, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ctx  Context
		want string
	}{
		{name: "inline", ctx: Context{AdminToken: "inline-token"}, want: "inline-token"},
		{name: "environment", ctx: Context{CredentialRef: "env:DEPLOYER_TEST_CONTEXT_TOKEN"}, want: "env-token"},
		{name: "file", ctx: Context{CredentialRef: "file:" + secretPath}, want: "file-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveContextCredential(tt.ctx)
			if err != nil || got != tt.want {
				t.Fatalf("ResolveContextCredential() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestContextCredentialRejectsInsecureFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveContextCredential(Context{CredentialRef: "file:" + path}); err == nil {
		t.Fatal("expected insecure credential file to be rejected")
	}
}
