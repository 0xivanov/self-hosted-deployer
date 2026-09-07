package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
)

func TestLoadServerIdentityStableAcrossRestart(t *testing.T) {
	database := filepath.Join(t.TempDir(), "deployer.db")
	cfg := config.ServerConfig{DatabaseURL: "file:" + database}
	first, err := LoadServerIdentity(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadServerIdentity(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("identity changed across restart: %q != %q", first, second)
	}
	info, err := os.Stat(database + ".identity")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("identity file permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadServerIdentityExplicitValue(t *testing.T) {
	identity, err := LoadServerIdentity(config.ServerConfig{ServerIdentity: "installation-a"})
	if err != nil || identity != "installation-a" {
		t.Fatalf("explicit identity = %q, %v", identity, err)
	}
}

func TestLoadServerIdentityRejectsExplicitValueAndFile(t *testing.T) {
	if _, err := LoadServerIdentity(config.ServerConfig{ServerIdentity: "installation-a", ServerIdentityFile: "/tmp/identity"}); err == nil {
		t.Fatal("expected conflicting identity configuration to fail")
	}
}

func TestSidecarPathAcceptsSQLiteQueryAndEscapedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db file.sqlite")
	got := sidecarPath("file:" + path + "?cache=shared&_pragma=busy_timeout(5000)")
	if got != path+".identity" {
		t.Fatalf("sidecar path = %q, want %q", got, path+".identity")
	}
	if sidecarPath("file::memory:?cache=shared") != "" {
		t.Fatal("memory database must not receive a persistent identity")
	}
	if got := sidecarPath(path + "?cache=shared"); got != path+".identity" {
		t.Fatalf("plain DSN sidecar path = %q, want %q", got, path+".identity")
	}
}

func TestLoadServerIdentityRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "identity")
	if err := os.WriteFile(target, []byte("identity\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateIdentityFile(path); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestIdentityConcurrentPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	const count = 32
	var wg sync.WaitGroup
	values := make([]string, count)
	errs := make([]error, count)
	for i := range count {
		wg.Add(1)
		go func() { defer wg.Done(); values[i], errs[i] = loadOrCreateIdentityFile(path) }()
	}
	wg.Wait()
	for i := range count {
		if errs[i] != nil || len(values[i]) != 64 || values[i] != values[0] {
			t.Fatalf("identity publication inconsistent at %d: %v", i, errs[i])
		}
	}
}

func TestIdentityRejectsSpecialFile(t *testing.T) {
	if _, err := loadOrCreateIdentityFile(os.DevNull); err == nil {
		t.Fatal("expected special file rejection")
	}
}

func TestIdentityURIPathsDecodeExactlyOnce(t *testing.T) {
	for input, want := range map[string]string{
		"file:/tmp/a%2520.db?cache=shared":     "/tmp/a%20.db.identity",
		"file:relative%20name.db?cache=shared": "relative name.db.identity",
		"file:/tmp/a.db?mode=memory":           "",
	} {
		if got := sidecarPath(input); got != want {
			t.Fatalf("%s: got %q want %q", input, got, want)
		}
	}
}
