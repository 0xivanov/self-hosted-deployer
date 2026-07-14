package appconfig

import (
	"reflect"
	"strings"
	"testing"
)

const validPostgresDigest = "ghcr.io/cloudnative-pg/postgresql:17@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

const validPostgresYAML = `
name: my-api
image: ivan/my-api:1.0.0
service:
  port: 3000
  health:
    path: /health
routing: {}
deploy:
  replicas: 2
placement: {}
secrets: []
state:
  mode: stateless
database:
  postgres:
    instances: 3
    image: ` + validPostgresDigest + `
    database: money_manager
    owner: money_manager
    connectionEnv: DATABASE_URL
    connectionMode: managed
    storage:
      size: 10Gi
      storageClass: local-path
    synchronous:
      replicas: 1
      dataDurability: required
    retentionPolicy: retain
`

func TestParsePostgresAppliesDefaultsWithoutChangingStateMode(t *testing.T) {
	t.Parallel()

	cfg, err := Parse([]byte(`
name: my-api
image: ivan/my-api:1.0.0
service:
  port: 3000
  health:
    path: /health
routing: {}
deploy:
  replicas: 2
placement: {}
database:
  postgres:
    image: ` + validPostgresDigest + `
    database: money_manager
    owner: money_manager
    storage:
      size: 10Gi
`))
	if err != nil {
		t.Fatalf("parse Postgres config: %v", err)
	}
	if cfg.Database.Postgres == nil {
		t.Fatal("expected Postgres config")
	}

	postgres := cfg.Database.Postgres
	if postgres.Instances != DefaultPostgresInstances {
		t.Errorf("instances = %d, want %d", postgres.Instances, DefaultPostgresInstances)
	}
	if postgres.ConnectionEnv != DefaultPostgresConnectionEnv {
		t.Errorf("connectionEnv = %q, want %q", postgres.ConnectionEnv, DefaultPostgresConnectionEnv)
	}
	if postgres.ConnectionMode != PostgresConnectionModeManaged {
		t.Errorf("connectionMode = %q, want %q", postgres.ConnectionMode, PostgresConnectionModeManaged)
	}
	if postgres.Storage.StorageClass != DefaultPostgresStorageClass {
		t.Errorf("storageClass = %q, want %q", postgres.Storage.StorageClass, DefaultPostgresStorageClass)
	}
	if postgres.Synchronous.Replicas != DefaultPostgresSyncReplicas {
		t.Errorf("synchronous replicas = %d, want %d", postgres.Synchronous.Replicas, DefaultPostgresSyncReplicas)
	}
	if postgres.Synchronous.DataDurability != PostgresDataDurabilityRequired {
		t.Errorf(
			"dataDurability = %q, want %q",
			postgres.Synchronous.DataDurability,
			PostgresDataDurabilityRequired,
		)
	}
	if postgres.RetentionPolicy != PostgresRetentionPolicyRetain {
		t.Errorf("retentionPolicy = %q, want %q", postgres.RetentionPolicy, PostgresRetentionPolicyRetain)
	}
	if cfg.State.Mode != DefaultStateMode {
		t.Errorf("state.mode = %q, want unchanged default %q", cfg.State.Mode, DefaultStateMode)
	}
}

func TestParsePostgresAcceptsExternalConnectionSecret(t *testing.T) {
	t.Parallel()

	body := strings.Replace(validPostgresYAML, "secrets: []", "secrets:\n  - EXTERNAL_DATABASE_URL", 1)
	body = strings.Replace(body, "connectionEnv: DATABASE_URL", "connectionEnv: EXTERNAL_DATABASE_URL", 1)
	body = strings.Replace(body, "connectionMode: managed", "connectionMode: external", 1)

	cfg, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse external Postgres config: %v", err)
	}
	if cfg.Database.Postgres.ConnectionMode != PostgresConnectionModeExternal {
		t.Fatalf("connectionMode = %q, want external", cfg.Database.Postgres.ConnectionMode)
	}
}

func TestParsePostgresAcceptsSupportedBounds(t *testing.T) {
	t.Parallel()

	body := strings.Replace(validPostgresYAML, "name: my-api", "name: "+strings.Repeat("a", 47), 1)
	body = strings.Replace(body, "placement: {}", "placement:\n  arch: linux/amd64", 1)
	body = strings.Replace(body, "instances: 3", "instances: 9", 1)
	body = strings.Replace(body, "size: 10Gi", "size: 1Gi", 1)
	body = strings.Replace(body, "replicas: 1\n      dataDurability", "replicas: 8\n      dataDurability", 1)
	body = strings.Replace(body, "dataDurability: required", "dataDurability: preferred", 1)

	cfg, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse supported Postgres bounds: %v", err)
	}
	if cfg.Database.Postgres.Synchronous.DataDurability != PostgresDataDurabilityPreferred {
		t.Fatalf(
			"dataDurability = %q, want preferred",
			cfg.Database.Postgres.Synchronous.DataDurability,
		)
	}
}

func TestParsePostgresValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "generated resource name too long",
			body: strings.Replace(validPostgresYAML, "name: my-api", "name: "+strings.Repeat("a", 48), 1),
			want: "generated name",
		},
		{
			name: "generated resource name starts with a digit",
			body: strings.Replace(validPostgresYAML, "name: my-api", "name: 1my-api", 1),
			want: "must start with a lowercase letter",
		},
		{
			name: "unsupported database architecture",
			body: strings.Replace(validPostgresYAML, "placement: {}", "placement:\n  arch: linux/riscv64", 1),
			want: "placement.arch to be one of linux/amd64, linux/arm64",
		},
		{
			name: "too few instances",
			body: strings.Replace(validPostgresYAML, "instances: 3", "instances: 2", 1),
			want: "instances must be between 3 and 9",
		},
		{
			name: "too many instances",
			body: strings.Replace(validPostgresYAML, "instances: 3", "instances: 10", 1),
			want: "instances must be between 3 and 9",
		},
		{
			name: "mutable image",
			body: strings.Replace(validPostgresYAML, validPostgresDigest, "postgres:17", 1),
			want: "version tag and immutable sha256 digest",
		},
		{
			name: "digest without version tag",
			body: strings.Replace(validPostgresYAML, validPostgresDigest, "ghcr.io/cloudnative-pg/postgresql@sha256:"+strings.Repeat("a", 64), 1),
			want: "version tag and immutable sha256 digest",
		},
		{
			name: "non-version image tag",
			body: strings.Replace(validPostgresYAML, validPostgresDigest, "ghcr.io/cloudnative-pg/postgresql:stable@sha256:"+strings.Repeat("a", 64), 1),
			want: "version tag and immutable sha256 digest",
		},
		{
			name: "unsupported PostgreSQL major",
			body: strings.Replace(validPostgresYAML, validPostgresDigest, "ghcr.io/cloudnative-pg/postgresql:13.22@sha256:"+strings.Repeat("a", 64), 1),
			want: "major version must be between 14 and 18",
		},
		{
			name: "uppercase image digest",
			body: strings.Replace(validPostgresYAML, validPostgresDigest, strings.TrimSuffix(validPostgresDigest, "a")+"A", 1),
			want: "version tag and immutable sha256 digest",
		},
		{
			name: "invalid database identifier",
			body: strings.Replace(validPostgresYAML, "database: money_manager", "database: Money-Manager", 1),
			want: "database must be a valid unquoted PostgreSQL identifier",
		},
		{
			name: "reserved postgres database",
			body: strings.Replace(validPostgresYAML, "database: money_manager", "database: postgres", 1),
			want: `database "postgres" is reserved`,
		},
		{
			name: "reserved template database",
			body: strings.Replace(validPostgresYAML, "database: money_manager", "database: template1", 1),
			want: `database "template1" is reserved`,
		},
		{
			name: "invalid owner identifier",
			body: strings.Replace(validPostgresYAML, "owner: money_manager", "owner: 9owner", 1),
			want: "owner must be a valid unquoted PostgreSQL identifier",
		},
		{
			name: "postgres superuser owner",
			body: strings.Replace(validPostgresYAML, "owner: money_manager", "owner: postgres", 1),
			want: `owner "postgres" is reserved`,
		},
		{
			name: "CloudNativePG system owner",
			body: strings.Replace(validPostgresYAML, "owner: money_manager", "owner: cnpg_metrics_exporter", 1),
			want: `owner "cnpg_metrics_exporter" is reserved`,
		},
		{
			name: "PostgreSQL system owner prefix",
			body: strings.Replace(validPostgresYAML, "owner: money_manager", "owner: pg_monitor", 1),
			want: `owner "pg_monitor" is reserved`,
		},
		{
			name: "streaming replication owner",
			body: strings.Replace(validPostgresYAML, "owner: money_manager", "owner: streaming_replica", 1),
			want: `owner "streaming_replica" is reserved`,
		},
		{
			name: "invalid connection environment name",
			body: strings.Replace(validPostgresYAML, "connectionEnv: DATABASE_URL", "connectionEnv: database-url", 1),
			want: "connectionEnv",
		},
		{
			name: "invalid connection mode",
			body: strings.Replace(validPostgresYAML, "connectionMode: managed", "connectionMode: automatic", 1),
			want: "connectionMode must be one of external, managed",
		},
		{
			name: "managed connection conflicts with app secret",
			body: strings.Replace(validPostgresYAML, "secrets: []", "secrets:\n  - DATABASE_URL", 1),
			want: "must not also appear in secrets for managed mode",
		},
		{
			name: "managed connection environment conflicts with TLS mode",
			body: strings.Replace(validPostgresYAML, "connectionEnv: DATABASE_URL", "connectionEnv: PGSSLMODE", 1),
			want: `connectionEnv "PGSSLMODE" is reserved for managed PostgreSQL transport security`,
		},
		{
			name: "managed TLS mode conflicts with app secret",
			body: strings.Replace(validPostgresYAML, "secrets: []", "secrets:\n  - PGSSLMODE", 1),
			want: `secret "PGSSLMODE" is reserved for managed PostgreSQL transport security`,
		},
		{
			name: "managed channel binding conflicts with app secret",
			body: strings.Replace(validPostgresYAML, "secrets: []", "secrets:\n  - PGCHANNELBINDING", 1),
			want: `secret "PGCHANNELBINDING" is reserved for managed PostgreSQL transport security`,
		},
		{
			name: "managed authentication requirement conflicts with app secret",
			body: strings.Replace(validPostgresYAML, "secrets: []", "secrets:\n  - PGREQUIREAUTH", 1),
			want: `secret "PGREQUIREAUTH" is reserved for managed PostgreSQL transport security`,
		},
		{
			name: "external connection is missing app secret",
			body: strings.Replace(validPostgresYAML, "connectionMode: managed", "connectionMode: external", 1),
			want: "must appear in secrets for external mode",
		},
		{
			name: "invalid storage quantity",
			body: strings.Replace(validPostgresYAML, "size: 10Gi", "size: a-lot", 1),
			want: "valid positive Kubernetes quantity",
		},
		{
			name: "negative storage quantity",
			body: strings.Replace(validPostgresYAML, "size: 10Gi", "size: -1Gi", 1),
			want: "valid positive Kubernetes quantity",
		},
		{
			name: "storage below minimum",
			body: strings.Replace(validPostgresYAML, "size: 10Gi", "size: 512Mi", 1),
			want: "storage.size must be at least 1Gi",
		},
		{
			name: "invalid storage class",
			body: strings.Replace(validPostgresYAML, "storageClass: local-path", "storageClass: Bad_Class", 1),
			want: "storage.storageClass must be a DNS-safe name",
		},
		{
			name: "synchronous replicas equal instances",
			body: strings.Replace(validPostgresYAML, "replicas: 1\n      dataDurability", "replicas: 3\n      dataDurability", 1),
			want: "synchronous.replicas must be at least 1 and less than instances",
		},
		{
			name: "invalid data durability",
			body: strings.Replace(validPostgresYAML, "dataDurability: required", "dataDurability: eventual", 1),
			want: "dataDurability must be one of required, preferred",
		},
		{
			name: "unsupported retention policy",
			body: strings.Replace(validPostgresYAML, "retentionPolicy: retain", "retentionPolicy: delete", 1),
			want: "retentionPolicy must be retain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse([]byte(tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestParsePostgresRejectsUnknownNestedFields(t *testing.T) {
	t.Parallel()

	body := strings.Replace(validPostgresYAML, "    retentionPolicy: retain", "    retentionPolicy: retain\n    failoverMagic: true", 1)
	_, err := Parse([]byte(body))
	if err == nil || !strings.Contains(err.Error(), "field failoverMagic not found") {
		t.Fatalf("expected unknown nested field error, got %v", err)
	}
}

func TestPostgresJSONRoundTrip(t *testing.T) {
	t.Parallel()

	cfg, err := Parse([]byte(validPostgresYAML))
	if err != nil {
		t.Fatalf("parse Postgres config: %v", err)
	}

	data, err := cfg.JSON()
	if err != nil {
		t.Fatalf("encode Postgres config: %v", err)
	}
	roundTripped, err := FromJSON(data)
	if err != nil {
		t.Fatalf("decode Postgres config: %v", err)
	}
	if err := roundTripped.Validate(); err != nil {
		t.Fatalf("validate round-tripped Postgres config: %v", err)
	}
	if !reflect.DeepEqual(roundTripped, cfg) {
		t.Fatalf("round-tripped config differs:\n got: %#v\nwant: %#v", roundTripped, cfg)
	}
}

func TestPostgresBlockIsOptional(t *testing.T) {
	t.Parallel()

	cfg, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("parse config without Postgres: %v", err)
	}
	if cfg.Database.Postgres != nil {
		t.Fatalf("expected no Postgres config, got %#v", cfg.Database.Postgres)
	}
}
