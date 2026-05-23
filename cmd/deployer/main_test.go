package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func TestHelpShowsGlobalFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := runWithIO([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	help := stderr.String()
	for _, want := range []string{"--server", "--token", "--config", "--output"} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected help to contain %q, got:\n%s", want, help)
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout for help, got %q", stdout.String())
	}
}

func TestUnknownCommandReturnsUsageError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := runWithIO([]string{"bogus"}, &stdout, &stderr); code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}

	if !strings.Contains(stderr.String(), `unknown command "bogus"`) {
		t.Fatalf("expected unknown command error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout for command error, got %q", stdout.String())
	}
}

func TestVersionSupportsJSONOutput(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := runWithIO([]string{"--output", "json", "version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	var got map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("expected JSON version output: %v", err)
	}
	if got["version"] == "" || got["commit"] == "" || got["build_date"] == "" {
		t.Fatalf("expected version fields, got %#v", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr for version, got %q", stderr.String())
	}
}

func TestInvalidOutputFormatReturnsUsageError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := runWithIO([]string{"--output", "yaml", "version"}, &stdout, &stderr); code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}

	if !strings.Contains(stderr.String(), `unsupported output format "yaml"`) {
		t.Fatalf("expected output validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout for validation error, got %q", stdout.String())
	}
}

func TestLoginPromptsForTokenValidatesAndSavesConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	factory := &recordingClientFactory{
		status: clicore.ServerStatus{Version: "dev", Ready: true},
	}
	app := newCLIApp(strings.NewReader("dep_admin_prompted\n"), &stdout, &stderr)
	app.newPlatformClient = factory.newClient

	code := app.run([]string{"--config", configPath, "login", "http://localhost:7443/"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s", code, stderr.String())
	}
	if factory.serverURL != "http://localhost:7443" {
		t.Fatalf("expected normalized server URL, got %q", factory.serverURL)
	}
	if factory.token != "dep_admin_prompted" {
		t.Fatalf("expected prompted token, got %q", factory.token)
	}

	cfg, err := clicore.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if cfg.ServerURL != "http://localhost:7443" || cfg.AdminToken != "dep_admin_prompted" || cfg.Output != clicore.OutputHuman {
		t.Fatalf("unexpected saved config: %#v", cfg)
	}
	if !strings.Contains(stdout.String(), "logged in to http://localhost:7443") {
		t.Fatalf("expected login success output, got %q", stdout.String())
	}
}

func TestLoginPreservesExistingOutputWhenOutputFlagUnset(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := clicore.SaveConfig(configPath, clicore.Config{
		ServerURL:  "old:7443",
		AdminToken: "dep_admin_old",
		Output:     clicore.OutputJSON,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	factory := &recordingClientFactory{
		status: clicore.ServerStatus{Version: "dev", Ready: true},
	}
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	app.newPlatformClient = factory.newClient

	code := app.run([]string{"--config", configPath, "--token", "dep_admin_new", "login", "localhost:7443"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s", code, stderr.String())
	}

	cfg, err := clicore.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if cfg.Output != clicore.OutputJSON {
		t.Fatalf("expected login to preserve JSON output, got %q", cfg.Output)
	}
	if cfg.ServerURL != "localhost:7443" || cfg.AdminToken != "dep_admin_new" {
		t.Fatalf("unexpected saved config: %#v", cfg)
	}
}

func TestServerStatusUsesConfigAndSupportsJSON(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := clicore.SaveConfig(configPath, clicore.Config{
		ServerURL:  "localhost:7443",
		AdminToken: "dep_admin_saved",
		Output:     clicore.OutputHuman,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	factory := &recordingClientFactory{
		status: clicore.ServerStatus{
			Version:   "1.2.3",
			Commit:    "abc123",
			BuildDate: "2026-05-23T00:00:00Z",
			Ready:     true,
		},
	}
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	app.newPlatformClient = factory.newClient

	code := app.run([]string{"--config", configPath, "--output", "json", "server", "status"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s", code, stderr.String())
	}
	if factory.serverURL != "localhost:7443" || factory.token != "dep_admin_saved" {
		t.Fatalf("expected config credentials, got server=%q token=%q", factory.serverURL, factory.token)
	}

	var got clicore.ServerStatus
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal status output: %v", err)
	}
	if got.Version != "1.2.3" || !got.Ready {
		t.Fatalf("unexpected status output: %#v", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestServerStatusFlagsOverrideConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := clicore.SaveConfig(configPath, clicore.Config{
		ServerURL:  "saved:7443",
		AdminToken: "dep_admin_saved",
		Output:     clicore.OutputHuman,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	factory := &recordingClientFactory{
		status: clicore.ServerStatus{Version: "dev", Ready: true},
	}
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	app.newPlatformClient = factory.newClient

	code := app.run([]string{
		"--config", configPath,
		"--server", "flagged:7443",
		"--token", "dep_admin_flagged",
		"server", "status",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s", code, stderr.String())
	}
	if factory.serverURL != "flagged:7443" || factory.token != "dep_admin_flagged" {
		t.Fatalf("expected flag credentials, got server=%q token=%q", factory.serverURL, factory.token)
	}
}

func TestServerStatusMissingConfigIsActionable(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)

	code := app.run([]string{"--config", filepath.Join(t.TempDir(), "missing.json"), "server", "status"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "run deployer login <server-url>") {
		t.Fatalf("expected actionable missing config error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout, got %q", stdout.String())
	}
}

type recordingClientFactory struct {
	serverURL string
	token     string
	status    clicore.ServerStatus
	err       error
}

func (f *recordingClientFactory) newClient(serverURL string, token string) (platformClient, func() error, error) {
	f.serverURL = serverURL
	f.token = token
	return recordingClient{status: f.status, err: f.err}, func() error { return nil }, nil
}

type recordingClient struct {
	status clicore.ServerStatus
	err    error
}

func (c recordingClient) Status(context.Context) (clicore.ServerStatus, error) {
	if c.err != nil {
		return clicore.ServerStatus{}, c.err
	}
	return c.status, nil
}

func (c recordingClient) CreateJoinToken(context.Context, string, map[string]string) (clicore.JoinTokenResult, error) {
	return clicore.JoinTokenResult{}, c.err
}

func (c recordingClient) ListNodes(context.Context) ([]clicore.NodeInfo, error) {
	return nil, c.err
}

func (c recordingClient) GetNode(context.Context, string) (clicore.NodeInfo, error) {
	return clicore.NodeInfo{}, c.err
}

func TestLoginRejectsInvalidToken(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	factory := &recordingClientFactory{
		err: errors.New("authentication failed: invalid bearer token"),
	}
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	app.newPlatformClient = factory.newClient

	code := app.run([]string{"--token", "dep_admin_bad", "login", "localhost:7443"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "authentication failed") {
		t.Fatalf("expected authentication error, got %q", stderr.String())
	}
}
