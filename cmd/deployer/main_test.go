package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
