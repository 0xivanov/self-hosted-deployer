package appconfig

import (
	"strings"
	"testing"
)

const validYAML = `
name: my-api
image: ivan/my-api:1.0.0
service:
  port: 3000
  health:
    path: /health
routing:
  domain: api.example.com
deploy:
  replicas: 2
placement:
  spread: true
  prefer:
    - location: home
  fallback:
    - location: vps
secrets:
  - DATABASE_URL
state:
  mode: stateless
`

func TestParseValidConfigAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
name: my-api
image: ivan/my-api:1.0.0
service:
  port: 3000
  health:
    path: /health
routing:
  domain: api.example.com
deploy:
  replicas: 2
placement:
  spread: true
`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Placement.Arch != DefaultPlacementArch {
		t.Fatalf("expected default placement arch, got %q", cfg.Placement.Arch)
	}
	if cfg.Deploy.Strategy != DefaultDeployStrategy {
		t.Fatalf("expected default deploy strategy, got %q", cfg.Deploy.Strategy)
	}
	if cfg.State.Mode != DefaultStateMode {
		t.Fatalf("expected default state mode, got %q", cfg.State.Mode)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse([]byte(validYAML + "\nunexpected: true\n"))
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestValidateIdentifiesExactFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing app name",
			body: strings.Replace(validYAML, "name: my-api\n", "", 1),
			want: "name is required",
		},
		{
			name: "bad app name",
			body: strings.Replace(validYAML, "name: my-api", "name: My_API", 1),
			want: "name must be a DNS-safe Kubernetes name",
		},
		{
			name: "empty image",
			body: strings.Replace(validYAML, "image: ivan/my-api:1.0.0", "image: \"\"", 1),
			want: "image is required",
		},
		{
			name: "bad port",
			body: strings.Replace(validYAML, "port: 3000", "port: 70000", 1),
			want: "service.port must be between 1 and 65535",
		},
		{
			name: "bad health path",
			body: strings.Replace(validYAML, "path: /health", "path: health", 1),
			want: "service.health.path must start with /",
		},
		{
			name: "bad replicas",
			body: strings.Replace(validYAML, "replicas: 2", "replicas: 0", 1),
			want: "deploy.replicas must be at least 1",
		},
		{
			name: "bad state mode",
			body: strings.Replace(validYAML, "mode: stateless", "mode: durable", 1),
			want: "state.mode must be one of stateless, stateful, cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}
