package logging

import "testing"

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
