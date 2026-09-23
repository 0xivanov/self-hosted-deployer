package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadEnvironmentValuesRejectsDuplicateKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.json")
	if err := os.WriteFile(path, []byte(`{"A":"one","A":"two"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvironmentValuesFile(path); err == nil {
		t.Fatal("accepted duplicate environment key")
	}
}

func TestReadEnvironmentValuesAllowsEmptyMapAndEmptyValue(t *testing.T) {
	for _, content := range []string{`{}`, `{"EMPTY":""}`} {
		path := filepath.Join(t.TempDir(), "values.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		values, err := readEnvironmentValuesFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", content, err)
		}
		if content == `{}` && len(values) != 0 {
			t.Fatalf("unexpected values: %#v", values)
		}
	}
}
