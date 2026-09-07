package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func targetContextConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	contexts := map[string]clicore.Context{
		"customer-a": {ServerURL: "https://customer-a.example:7443", AdminToken: "dep_admin_a"},
	}
	if err := clicore.SaveConfig(path, clicore.Config{Contexts: &contexts, CurrentContext: "customer-a", Output: clicore.OutputHuman}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSecretRemovalAnnouncesTargetBeforeConfirmation(t *testing.T) {
	configPath := targetContextConfig(t)
	var stdout, stderr bytes.Buffer
	app := newCLIApp(strings.NewReader("n\n"), &stdout, &stderr)
	app.newPlatformClient = func(string, string) (platformClient, func() error, error) {
		return recordingClient{}, func() error { return nil }, nil
	}
	if code := app.run([]string{"--config", configPath, "secrets", "remove", "my-api", "DATABASE_URL"}); code != 0 {
		t.Fatalf("expected cancellation success, got %d: %s", code, stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "Target: context customer-a (https://customer-a.example:7443)\n") {
		t.Fatalf("target was not announced first: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Remove secret DATABASE_URL") {
		t.Fatalf("confirmation prompt missing: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("cancellation should not write stdout: %q", stdout.String())
	}
}

func TestSecretSetJSONKeepsTargetAnnouncementOffStdout(t *testing.T) {
	configPath := targetContextConfig(t)
	var stdout, stderr bytes.Buffer
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	app.newPlatformClient = func(string, string) (platformClient, func() error, error) {
		return recordingClient{}, func() error { return nil }, nil
	}
	if code := app.run([]string{"--config", configPath, "--output", "json", "secrets", "set", "--value", "redacted", "my-api", "DATABASE_URL"}); code != 0 {
		t.Fatalf("expected secret set success, got %d: %s", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON stdout, got %q: %v", stdout.String(), err)
	}
	if strings.Contains(stdout.String(), "customer-a") || strings.Contains(stdout.String(), "7443") {
		t.Fatalf("target leaked into JSON stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Target: context customer-a") {
		t.Fatalf("target announcement missing from stderr: %q", stderr.String())
	}
}
