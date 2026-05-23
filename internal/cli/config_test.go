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
