package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestRedactDoesNotReturnFullValue(t *testing.T) {
	value := "dep_admin_abcdefghijklmnopqrstuvwxyz"
	redacted := Redact(value)
	if redacted == value {
		t.Fatal("redaction returned full value")
	}
	if redacted == "" {
		t.Fatal("redaction returned empty value")
	}
}

func TestHandlerOmitsTimestamp(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(newHandler(&output, "debug")).With("component", "test")

	logger.Info("hello")

	fields := map[string]any{}
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatalf("parse log output: %v", err)
	}
	if _, ok := fields["time"]; ok {
		t.Fatalf("expected no time field, got %v", fields["time"])
	}
}
