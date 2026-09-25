package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func TestAppsWithdrawAcceptsLeadingAndTrailingFileFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "original.yaml")
	data := testDeployYAML("example/my-api:1")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	for _, args := range [][]string{
		{"my-api", id, "--file", path},
		{"--file", path, "my-api", id},
	} {
		var stdout, stderr bytes.Buffer
		app := cliApp{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
		client := &recordingAppClient{}
		if code := app.appsWithdraw(args, runtimeOptions{output: clicore.OutputJSON}, client); code != 0 {
			t.Fatalf("withdraw command failed for %v: code=%d stderr=%q", args, code, stderr.String())
		}
		if client.withdrawRequestID != id || client.withdrawYAML != data {
			t.Fatalf("withdraw request mismatch: id=%q yaml=%q", client.withdrawRequestID, client.withdrawYAML)
		}
	}
}

func TestAppsWithdrawRejectsConfigAppMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "original.yaml")
	if err := os.WriteFile(path, []byte(strings.Replace(testDeployYAML("example/other:1"), "name: my-api", "name: other-app", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app := cliApp{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
	client := &recordingAppClient{}
	if code := app.appsWithdraw([]string{"my-api", strings.Repeat("a", 64), "--file", path}, runtimeOptions{}, client); code != 2 || client.withdrawRequestID != "" {
		t.Fatalf("mismatched configuration was accepted: code=%d stderr=%q", code, stderr.String())
	}
}
