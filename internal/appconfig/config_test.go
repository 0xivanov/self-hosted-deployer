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
	if cfg.Resilience.Mode != DefaultResilienceMode {
		t.Fatalf("expected default resilience mode, got %q", cfg.Resilience.Mode)
	}
}

func TestParseAllowsAppWithoutRoutingDomain(t *testing.T) {
	cfg, err := Parse([]byte(`
name: worker
image: ivan/worker:1.0.0
service:
  port: 3000
  health:
    path: /health
routing: {}
deploy:
  replicas: 1
placement: {}
`))
	if err != nil {
		t.Fatalf("parse config without routing domain: %v", err)
	}
	if cfg.Routing.Domain != "" {
		t.Fatalf("expected empty routing domain, got %q", cfg.Routing.Domain)
	}
}

func TestParseAllowsInternalMetricsEndpoint(t *testing.T) {
	cfg, err := Parse([]byte(`
name: my-api
image: ivan/my-api:1.0.0
service:
  port: 3000
  health:
    path: /health
metrics:
  port: 9090
  path: /metrics
routing: {}
deploy:
  replicas: 1
placement: {}
`))
	if err != nil {
		t.Fatalf("parse metrics config: %v", err)
	}
	if cfg.Metrics == nil || cfg.Metrics.Port != 9090 || cfg.Metrics.Path != "/metrics" {
		t.Fatalf("unexpected metrics config: %#v", cfg.Metrics)
	}
}

func TestParseAllowsOptInReadOnlyRootFilesystem(t *testing.T) {
	cfg, err := Parse([]byte(`
name: hosted-api
image: example/hosted-api:1.0.0
service:
  port: 8080
  health:
    path: /readyz
routing: {}
deploy:
  replicas: 1
placement: {}
hosting:
  version: v1
  readOnlyRootFilesystem: true
  maxReplicas: 1
  resources:
    requests: {cpu: 100m, memory: 128Mi, ephemeralStorage: 256Mi}
    limits: {cpu: 500m, memory: 512Mi, ephemeralStorage: 1Gi}
`))
	if err != nil {
		t.Fatalf("parse read-only root filesystem: %v", err)
	}
	if cfg.Hosting == nil || !cfg.Hosting.ReadOnlyRootFilesystem {
		t.Fatalf("readOnlyRootFilesystem was not preserved: %#v", cfg.Hosting)
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
			name: "metrics shares public port",
			body: validYAML + "\nmetrics:\n  port: 3000\n  path: /metrics\n",
			want: "metrics.port must differ from service.port",
		},
		{
			name: "bad metrics path",
			body: validYAML + "\nmetrics:\n  port: 9090\n  path: metrics\n",
			want: "metrics.path must start with /",
		},
		{
			name: "bad replicas",
			body: strings.Replace(validYAML, "replicas: 2", "replicas: 0", 1),
			want: "deploy.replicas must be at least 1",
		},
		{
			name: "required TLS without domain",
			body: strings.Replace(validYAML, "domain: api.example.com", "domain: \"\"\n  requireTLS: true", 1),
			want: "routing.requireTLS requires routing.domain",
		},
		{
			name: "bad state mode",
			body: strings.Replace(validYAML, "mode: stateless", "mode: durable", 1),
			want: "state.mode must be one of stateless, stateful, cache",
		},
		{
			name: "bad resilience mode",
			body: validYAML + "\nresilience:\n  mode: impossible\n",
			want: "resilience.mode must be one of basic, resilient, fallback, pinned",
		},
		{
			name: "unsafe stateful failover",
			body: strings.Replace(validYAML, "mode: stateless", "mode: stateful", 1) + "\nresilience:\n  mode: resilient\n",
			want: "allowUnsafeStatefulFailover",
		},
		{
			name: "bad secret environment name",
			body: strings.Replace(validYAML, "DATABASE_URL", "database-url", 1),
			want: "must be a valid environment variable name",
		},
		{
			name: "duplicate secret name",
			body: strings.Replace(validYAML, "  - DATABASE_URL", "  - DATABASE_URL\n  - DATABASE_URL", 1),
			want: "duplicate secret name",
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

func TestParseRetainedStorageConfig(t *testing.T) {
	base := `
name: homephotos
image: ghcr.io/example/homephotos:1.0.0
service:
  port: 8080
  health:
    path: /health
routing:
  domain: photos.example.com
  requireTLS: true
deploy:
  replicas: 1
placement:
  prefer:
    - node-id: node-home
state:
  mode: stateful
resilience:
  mode: pinned
storage:
  existingClaim: homephotos-data
  mountPath: /var/lib/homephotos
`
	cfg, err := Parse([]byte(base))
	if err != nil {
		t.Fatalf("parse retained storage config: %v", err)
	}
	if cfg.Storage == nil || cfg.Storage.ExistingClaim != "homephotos-data" ||
		cfg.Storage.MountPath != "/var/lib/homephotos" || cfg.Deploy.Strategy != DeployStrategyRecreate {
		t.Fatalf("unexpected retained storage config: %#v", cfg)
	}
	encoded, err := cfg.JSON()
	if err != nil {
		t.Fatalf("encode retained storage config: %v", err)
	}
	roundTripped, err := FromJSON(encoded)
	if err != nil || roundTripped.Storage == nil || roundTripped.Storage.ExistingClaim != "homephotos-data" ||
		!roundTripped.Routing.RequireTLS {
		t.Fatalf("retained storage config did not round trip: %#v, %v", roundTripped, err)
	}
}

func TestParseRejectsUnsafeRetainedStorageConfig(t *testing.T) {
	valid := `
name: homephotos
image: ghcr.io/example/homephotos:1.0.0
service: {port: 8080, health: {path: /health}}
routing: {}
deploy: {replicas: 1, strategy: recreate}
placement:
  prefer:
    - node-id: node-home
state: {mode: stateful}
resilience: {mode: pinned}
storage: {existingClaim: homephotos-data, mountPath: /data}
`
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing claim",
			body: strings.Replace(valid, "homephotos-data", `""`, 1),
			want: "storage.existingClaim is required",
		},
		{name: "invalid claim", body: strings.Replace(valid, "homephotos-data", "Home_Photos", 1), want: "DNS-safe"},
		{name: "relative mount", body: strings.Replace(valid, "/data", "data", 1), want: "clean absolute path"},
		{name: "root mount", body: strings.Replace(valid, "/data", "/", 1), want: "other than /"},
		{name: "stateless", body: strings.Replace(valid, "stateful", "stateless", 1), want: "state.mode stateful"},
		{name: "not pinned", body: strings.Replace(valid, "mode: pinned", "mode: basic", 1), want: "resilience.mode pinned"},
		{
			name: "multiple replicas",
			body: strings.Replace(valid, "replicas: 1", "replicas: 2", 1),
			want: "deploy.replicas 1",
		},
		{
			name: "rolling update",
			body: strings.Replace(valid, "strategy: recreate", "strategy: rolling", 1),
			want: "deploy.strategy recreate",
		},
		{
			name: "broad selector",
			body: strings.Replace(valid, "node-id: node-home", "location: home", 1),
			want: "exact node-id selector",
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

func TestParseAllowsExplicitStatefulFailoverOverride(t *testing.T) {
	body := strings.Replace(validYAML, "mode: stateless", "mode: stateful", 1) + `
resilience:
  mode: fallback
  allowUnsafeStatefulFailover: true
`
	cfg, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse explicitly allowed failover: %v", err)
	}
	if cfg.Resilience.Mode != ResilienceFallback || !cfg.Resilience.AllowUnsafeStatefulFailover {
		t.Fatalf("unexpected resilience config: %#v", cfg.Resilience)
	}
}
