package appconfig

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	DefaultPostgresInstances         = 3
	DefaultPostgresConnectionEnv     = "DATABASE_URL"
	DefaultPostgresStorageClass      = "local-path"
	DefaultPostgresSyncReplicas      = 1
	PostgresConnectionModeManaged    = "managed"
	PostgresConnectionModeExternal   = "external"
	PostgresDataDurabilityRequired   = "required"
	PostgresDataDurabilityPreferred  = "preferred"
	PostgresRetentionPolicyRetain    = "retain"
	managedPostgresSSLModeEnv        = "PGSSLMODE"
	managedPostgresChannelBindingEnv = "PGCHANNELBINDING"
	managedPostgresRequireAuthEnv    = "PGREQUIREAUTH"
)

var (
	postgresImagePattern      = regexp.MustCompile(`^[^@\s]+:([0-9]+)(?:\.[0-9]+)*(?:[A-Za-z_-][A-Za-z0-9_.-]*)?@sha256:[0-9a-f]{64}$`)
	postgresIdentifierPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	minimumPostgresStorage    = resource.MustParse("1Gi")
	reservedPostgresDatabases = map[string]struct{}{
		"postgres":  {},
		"template0": {},
		"template1": {},
	}
	reservedPostgresOwners = map[string]struct{}{
		"cnpg_pooler_pgbouncer": {},
		"postgres":              {},
		"streaming_replica":     {},
	}
)

type DatabaseConfig struct {
	Postgres *PostgresConfig `json:"postgres,omitempty" yaml:"postgres,omitempty"`
}

type PostgresConfig struct {
	Instances       int                       `json:"instances" yaml:"instances"`
	Image           string                    `json:"image" yaml:"image"`
	Database        string                    `json:"database" yaml:"database"`
	Owner           string                    `json:"owner" yaml:"owner"`
	ConnectionEnv   string                    `json:"connectionEnv" yaml:"connectionEnv"`
	ConnectionMode  string                    `json:"connectionMode" yaml:"connectionMode"`
	Storage         PostgresStorageConfig     `json:"storage" yaml:"storage"`
	Synchronous     PostgresSynchronousConfig `json:"synchronous" yaml:"synchronous"`
	RetentionPolicy string                    `json:"retentionPolicy" yaml:"retentionPolicy"`
}

type PostgresStorageConfig struct {
	Size         string `json:"size" yaml:"size"`
	StorageClass string `json:"storageClass" yaml:"storageClass"`
}

type PostgresSynchronousConfig struct {
	Replicas       int    `json:"replicas" yaml:"replicas"`
	DataDurability string `json:"dataDurability" yaml:"dataDurability"`
}

func (c *DatabaseConfig) normalize() {
	if c.Postgres == nil {
		return
	}

	postgres := c.Postgres
	postgres.Image = strings.TrimSpace(postgres.Image)
	postgres.Database = strings.TrimSpace(postgres.Database)
	postgres.Owner = strings.TrimSpace(postgres.Owner)
	postgres.ConnectionEnv = strings.TrimSpace(postgres.ConnectionEnv)
	postgres.ConnectionMode = strings.TrimSpace(postgres.ConnectionMode)
	postgres.Storage.Size = strings.TrimSpace(postgres.Storage.Size)
	postgres.Storage.StorageClass = strings.TrimSpace(postgres.Storage.StorageClass)
	postgres.Synchronous.DataDurability = strings.TrimSpace(postgres.Synchronous.DataDurability)
	postgres.RetentionPolicy = strings.TrimSpace(postgres.RetentionPolicy)

	if postgres.Instances == 0 {
		postgres.Instances = DefaultPostgresInstances
	}
	if postgres.ConnectionEnv == "" {
		postgres.ConnectionEnv = DefaultPostgresConnectionEnv
	}
	if postgres.ConnectionMode == "" {
		postgres.ConnectionMode = PostgresConnectionModeManaged
	}
	if postgres.Storage.StorageClass == "" {
		postgres.Storage.StorageClass = DefaultPostgresStorageClass
	}
	if postgres.Synchronous.Replicas == 0 {
		postgres.Synchronous.Replicas = DefaultPostgresSyncReplicas
	}
	if postgres.Synchronous.DataDurability == "" {
		postgres.Synchronous.DataDurability = PostgresDataDurabilityRequired
	}
	if postgres.RetentionPolicy == "" {
		postgres.RetentionPolicy = PostgresRetentionPolicyRetain
	}
}

func (c DatabaseConfig) validate(appName, placementArch string, secrets map[string]struct{}) error {
	if c.Postgres == nil {
		return nil
	}

	postgres := *c.Postgres
	if len(appName+"-db") > 50 {
		return fmt.Errorf("database.postgres generated name %q must not exceed CloudNativePG's 50-character limit", appName+"-db")
	}
	if problems := k8svalidation.IsDNS1035Label(appName + "-db"); len(problems) > 0 {
		return fmt.Errorf("database.postgres generated name %q must start with a lowercase letter and be a valid DNS label", appName+"-db")
	}
	switch placementArch {
	case "linux/amd64", "linux/arm64":
	default:
		return fmt.Errorf("database.postgres requires placement.arch to be one of linux/amd64, linux/arm64")
	}
	if postgres.Instances < 3 || postgres.Instances > 9 {
		return fmt.Errorf("database.postgres.instances must be between 3 and 9")
	}
	if _, err := PostgresImageMajor(postgres.Image); err != nil {
		return fmt.Errorf("database.postgres.image %w", err)
	}
	if !postgresIdentifierPattern.MatchString(postgres.Database) {
		return fmt.Errorf("database.postgres.database must be a valid unquoted PostgreSQL identifier")
	}
	if _, reserved := reservedPostgresDatabases[postgres.Database]; reserved {
		return fmt.Errorf("database.postgres.database %q is reserved by PostgreSQL", postgres.Database)
	}
	if !postgresIdentifierPattern.MatchString(postgres.Owner) {
		return fmt.Errorf("database.postgres.owner must be a valid unquoted PostgreSQL identifier")
	}
	if _, reserved := reservedPostgresOwners[postgres.Owner]; reserved ||
		strings.HasPrefix(postgres.Owner, "pg_") || strings.HasPrefix(postgres.Owner, "cnpg_") {
		return fmt.Errorf("database.postgres.owner %q is reserved and cannot be used as the application owner", postgres.Owner)
	}
	if err := ValidateSecretName(postgres.ConnectionEnv); err != nil {
		return fmt.Errorf("database.postgres.connectionEnv: %w", err)
	}
	if err := validatePostgresConnection(postgres, secrets); err != nil {
		return err
	}
	if err := validatePostgresStorage(postgres.Storage); err != nil {
		return err
	}
	if postgres.Synchronous.Replicas < 1 || postgres.Synchronous.Replicas >= postgres.Instances {
		return fmt.Errorf("database.postgres.synchronous.replicas must be at least 1 and less than instances")
	}
	switch postgres.Synchronous.DataDurability {
	case PostgresDataDurabilityRequired, PostgresDataDurabilityPreferred:
	default:
		return fmt.Errorf("database.postgres.synchronous.dataDurability must be one of required, preferred")
	}
	if postgres.RetentionPolicy != PostgresRetentionPolicyRetain {
		return fmt.Errorf("database.postgres.retentionPolicy must be retain")
	}

	return nil
}

// PostgresImageMajor returns the PostgreSQL major version encoded in a valid,
// immutable CloudNativePG operand image reference.
func PostgresImageMajor(image string) (int, error) {
	imageParts := postgresImagePattern.FindStringSubmatch(image)
	if len(imageParts) != 2 {
		return 0, fmt.Errorf("must include a PostgreSQL version tag and immutable sha256 digest")
	}
	majorVersion, err := strconv.Atoi(imageParts[1])
	if err != nil || majorVersion < 14 || majorVersion > 18 {
		return 0, fmt.Errorf("PostgreSQL major version must be between 14 and 18 for CloudNativePG 1.30")
	}
	return majorVersion, nil
}

func validatePostgresConnection(postgres PostgresConfig, secrets map[string]struct{}) error {
	_, connectionEnvIsSecret := secrets[postgres.ConnectionEnv]
	switch postgres.ConnectionMode {
	case PostgresConnectionModeManaged:
		reservedManagedEnvironments := []string{
			managedPostgresSSLModeEnv,
			managedPostgresChannelBindingEnv,
			managedPostgresRequireAuthEnv,
		}
		for _, name := range reservedManagedEnvironments {
			if postgres.ConnectionEnv == name {
				return fmt.Errorf(
					"database.postgres.connectionEnv %q is reserved for managed PostgreSQL transport security",
					postgres.ConnectionEnv,
				)
			}
		}
		if connectionEnvIsSecret {
			return fmt.Errorf(
				"database.postgres.connectionEnv %q must not also appear in secrets for managed mode",
				postgres.ConnectionEnv,
			)
		}
		for _, name := range reservedManagedEnvironments {
			if _, exists := secrets[name]; exists {
				return fmt.Errorf("secret %q is reserved for managed PostgreSQL transport security", name)
			}
		}
	case PostgresConnectionModeExternal:
		if !connectionEnvIsSecret {
			return fmt.Errorf(
				"database.postgres.connectionEnv %q must appear in secrets for external mode",
				postgres.ConnectionEnv,
			)
		}
	default:
		return fmt.Errorf("database.postgres.connectionMode must be one of external, managed")
	}

	return nil
}

func validatePostgresStorage(storage PostgresStorageConfig) error {
	quantity, err := resource.ParseQuantity(storage.Size)
	if err != nil {
		return fmt.Errorf("database.postgres.storage.size must be a valid positive Kubernetes quantity")
	}
	if quantity.Sign() <= 0 {
		return fmt.Errorf("database.postgres.storage.size must be a valid positive Kubernetes quantity")
	}
	if quantity.Cmp(minimumPostgresStorage) < 0 {
		return fmt.Errorf("database.postgres.storage.size must be at least 1Gi")
	}
	if storage.StorageClass == "" {
		return fmt.Errorf("database.postgres.storage.storageClass is required")
	}
	if problems := k8svalidation.IsDNS1123Subdomain(storage.StorageClass); len(problems) > 0 {
		return fmt.Errorf("database.postgres.storage.storageClass must be a DNS-safe name")
	}

	return nil
}
