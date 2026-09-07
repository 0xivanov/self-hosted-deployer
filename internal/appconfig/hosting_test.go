package appconfig

import (
	"strings"
	"testing"
)

func validHostingYAML() string {
	return `
name: hosted-api
image: example/hosted-api:1.0.0
service:
  port: 8080
  health:
    path: /health
routing: {}
deploy:
  replicas: 2
placement: {}
hosting:
  version: v1
  maxReplicas: 3
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
      ephemeralStorage: 1Gi
    limits:
      cpu: 500m
      memory: 512Mi
      ephemeralStorage: 2Gi
`
}

func TestHostingConfigValidatesAndRoundTrips(t *testing.T) {
	cfg, err := Parse([]byte(validHostingYAML()))
	if err != nil {
		t.Fatalf("parse hosting profile: %v", err)
	}
	if cfg.Hosting == nil || cfg.Hosting.Version != "v1" || cfg.Hosting.MaxReplicas != 3 {
		t.Fatalf("unexpected hosting profile: %#v", cfg.Hosting)
	}
	encoded, err := cfg.JSON()
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := FromJSON(encoded)
	if err != nil || roundTripped.Hosting == nil || roundTripped.Hosting.Resources.Limits.Memory != "512Mi" {
		t.Fatalf("hosting profile did not round trip: %#v, %v", roundTripped.Hosting, err)
	}
}

func TestHostingConfigRejectsInvalidResourceProfiles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) string
		want   string
	}{
		{"unsupported version", func(s string) string { return strings.Replace(s, "version: v1", "version: v2", 1) }, "hosting.version must be v1"},
		{"missing request", func(s string) string { return strings.Replace(s, "      cpu: 100m\n", "", 1) }, "hosting.resources.requests.cpu is required"},
		{"invalid quantity", func(s string) string { return strings.Replace(s, "      memory: 128Mi", "      memory: many", 1) }, "hosting.resources.requests.memory is invalid"},
		{"limit below request", func(s string) string { return strings.Replace(s, "      cpu: 500m", "      cpu: 50m", 1) }, "hosting.resources.limits.cpu must be at least the request"},
		{"zero request", func(s string) string { return strings.Replace(s, "cpu: 100m", "cpu: 0", 1) }, "must be greater than zero"},
		{"negative request", func(s string) string { return strings.Replace(s, "cpu: 100m", "cpu: -100m", 1) }, "must be greater than zero"},
		{"excessive cap", func(s string) string { return strings.Replace(s, "maxReplicas: 3", "maxReplicas: 1001", 1) }, "between 1 and 1000"},
		{"replica cap", func(s string) string { return strings.Replace(s, "maxReplicas: 3", "maxReplicas: 1", 1) }, "deploy.replicas 2 exceeds hosting.maxReplicas 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.mutate(validHostingYAML())))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}
