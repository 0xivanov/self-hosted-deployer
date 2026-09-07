package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func testContextsConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	contexts := map[string]clicore.Context{
		"customer-a": {ServerURL: "a:7443", AdminToken: "token-a", EnvironmentID: "env-a", CustomerLabel: "Alpha"},
		"customer-b": {ServerURL: "b:7443", AdminToken: "token-b", EnvironmentID: "env-b", CustomerLabel: "Beta"},
	}
	if err := clicore.SaveConfig(path, clicore.Config{Contexts: &contexts, CurrentContext: "customer-a", Output: clicore.OutputHuman}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestContextsListDoesNotPrintCredentials(t *testing.T) {
	configPath := testContextsConfig(t)
	var stdout, stderr bytes.Buffer
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	if code := app.run([]string{"--config", configPath, "contexts", "list"}); code != 0 {
		t.Fatalf("contexts list exit code %d: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "token-a") || strings.Contains(stdout.String(), "token-b") {
		t.Fatalf("context list leaked a credential: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "customer-a") || !strings.Contains(stdout.String(), "Alpha") {
		t.Fatalf("context list missing metadata: %q", stdout.String())
	}
}

func TestContextsUseChangesOnlyCurrentContext(t *testing.T) {
	configPath := testContextsConfig(t)
	var stdout, stderr bytes.Buffer
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	if code := app.run([]string{"--config", configPath, "contexts", "use", "customer-b"}); code != 0 {
		t.Fatalf("contexts use exit code %d: %s", code, stderr.String())
	}
	cfg, err := clicore.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CurrentContext != "customer-b" || (*cfg.Contexts)["customer-a"].ServerURL != "a:7443" {
		t.Fatalf("unexpected context selection: %#v", cfg)
	}
}

func TestUnknownContextFailsBeforeClientCreation(t *testing.T) {
	configPath := testContextsConfig(t)
	app := newCLIApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	called := false
	app.newPlatformClient = func(string, string) (platformClient, func() error, error) {
		called = true
		return nil, func() error { return nil }, nil
	}
	if code := app.run([]string{"--config", configPath, "--context", "missing", "server", "status"}); code != 1 {
		t.Fatalf("expected unknown context failure, got %d", code)
	}
	if called {
		t.Fatal("client was created for unknown context")
	}
}

func TestContextCannotReuseCredentialWithDifferentEndpoint(t *testing.T) {
	configPath := testContextsConfig(t)
	var stderr bytes.Buffer
	app := newCLIApp(strings.NewReader(""), &bytes.Buffer{}, &stderr)
	called := false
	app.newPlatformClient = func(string, string) (platformClient, func() error, error) {
		called = true
		return nil, func() error { return nil }, nil
	}
	if code := app.run([]string{"--config", configPath, "--context", "customer-a", "--server", "attacker:7443", "server", "status"}); code != 1 {
		t.Fatalf("expected endpoint override failure, got %d", code)
	}
	if called || !strings.Contains(stderr.String(), "cannot override a named context endpoint") {
		t.Fatalf("expected fail-closed endpoint handling, called=%t stderr=%q", called, stderr.String())
	}
}

func TestContextsListJSONIsCredentialFree(t *testing.T) {
	configPath := testContextsConfig(t)
	var stdout, stderr bytes.Buffer
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	if code := app.run([]string{"--config", configPath, "--output", "json", "contexts", "list"}); code != 0 {
		t.Fatalf("contexts list exit code %d: %s", code, stderr.String())
	}
	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, ok := entry["admin_token"]; ok {
			t.Fatalf("JSON context entry exposed admin token: %#v", entry)
		}
	}
}

func TestNamedLoginPreservesLegacyAndOtherCustomers(t *testing.T) {
	path := testContextsConfig(t)
	cfg, err := clicore.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerURL, cfg.AdminToken = "legacy:7443", "legacy-token"
	if err := clicore.SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	app := newCLIApp(strings.NewReader(""), &out, &stderr)
	factory := &recordingClientFactory{status: clicore.ServerStatus{Ready: true}}
	app.newPlatformClient = factory.newClient
	if code := app.run([]string{"--config", path, "--context", "customer-b", "--token", "rotated-b", "login", "https://b:7443"}); code != 0 {
		t.Fatalf("login: %s", stderr.String())
	}
	got, err := clicore.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerURL != "legacy:7443" || got.AdminToken != "legacy-token" || (*got.Contexts)["customer-a"] != (*cfg.Contexts)["customer-a"] {
		t.Fatal("named login changed another environment")
	}
	if (*got.Contexts)["customer-b"].CustomerLabel != "Beta" {
		t.Fatal("credential rotation discarded customer metadata")
	}
	if code := app.run([]string{"--config", path, "contexts", "use", "--legacy"}); code != 0 {
		t.Fatalf("select legacy: %s", stderr.String())
	}
	resolved, err := resolveRuntimeOptions(cliOptions{configPath: path})
	if err != nil || resolved.serverURL != "legacy:7443" || resolved.token != "legacy-token" {
		t.Fatalf("legacy selection: %+v, %v", resolved, err)
	}
}

func TestLegacyLoginRestoresLegacySelection(t *testing.T) {
	path := testContextsConfig(t)
	var out, stderr bytes.Buffer
	app := newCLIApp(strings.NewReader("new-token\n"), &out, &stderr)
	factory := &recordingClientFactory{status: clicore.ServerStatus{Ready: true}}
	app.newPlatformClient = factory.newClient
	if code := app.run([]string{"--config", path, "login", "https://legacy:7443"}); code != 0 {
		t.Fatalf("login: %s", stderr.String())
	}
	resolved, err := resolveRuntimeOptions(cliOptions{configPath: path})
	if err != nil || resolved.context != "" || resolved.serverURL != "https://legacy:7443" {
		t.Fatalf("login selected wrong target: %+v, %v", resolved, err)
	}
}
