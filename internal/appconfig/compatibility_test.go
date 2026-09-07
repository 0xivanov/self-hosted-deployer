package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatibilityFixturesFreezeConfigDefaults(t *testing.T) {
	entries, err := os.ReadDir("testdata/compatibility")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			input, err := os.ReadFile(filepath.Join("testdata/compatibility", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Parse(input)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			got, err := cfg.JSON()
			if err != nil {
				t.Fatalf("encode normalized config: %v", err)
			}
			want, err := os.ReadFile(filepath.Join("testdata/compatibility", entry.Name()[:len(entry.Name())-len(filepath.Ext(entry.Name()))]+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if got != strings.TrimSpace(string(want)) {
				t.Fatalf("normalized config changed:\n got  %s\n want %s", got, want)
			}
		})
	}
}
