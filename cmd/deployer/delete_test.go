package main

import (
	"bytes"
	"strings"
	"testing"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func TestDeleteAppRequiresYesAndRendersJSONResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	client := &recordingAppClient{deleteResult: clicore.DeleteAppResult{Name: "my-api", Deleted: true}}
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	app.newPlatformClient = func(string, string) (platformClient, func() error, error) {
		return client, func() error { return nil }, nil
	}

	if code := app.run([]string{"--server", "localhost:7443", "--token", "admin", "delete", "my-api"}); code != 2 {
		t.Fatalf("delete without --yes exit code = %d, want 2", code)
	}
	if client.deleteAppName != "" {
		t.Fatal("delete called without confirmation")
	}

	stdout.Reset()
	if code := app.run([]string{"--server", "localhost:7443", "--token", "admin", "--output", "json", "delete", "--yes", "my-api"}); code != 0 {
		t.Fatalf("delete exit code = %d, stderr=%q", code, stderr.String())
	}
	if client.deleteAppName != "my-api" {
		t.Fatalf("delete app name = %q", client.deleteAppName)
	}
	if !strings.Contains(stdout.String(), `"deleted": true`) || !strings.Contains(stdout.String(), `"name": "my-api"`) {
		t.Fatalf("unexpected JSON output: %q", stdout.String())
	}
}

func TestDeleteAppAlreadyAbsentIsSuccessful(t *testing.T) {
	var stdout, stderr bytes.Buffer
	client := &recordingAppClient{deleteResult: clicore.DeleteAppResult{Name: "missing", AlreadyAbsent: true}}
	app := newCLIApp(strings.NewReader(""), &stdout, &stderr)
	app.newPlatformClient = func(string, string) (platformClient, func() error, error) {
		return client, func() error { return nil }, nil
	}

	if code := app.run([]string{"--server", "localhost:7443", "--token", "admin", "--output", "json", "delete", "--yes", "missing"}); code != 0 {
		t.Fatalf("already absent delete exit code = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"already_absent": true`) {
		t.Fatalf("unexpected JSON output: %q", stdout.String())
	}
}
