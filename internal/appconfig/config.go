package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultPlacementArch  = "linux/arm64"
	DefaultDeployStrategy = "rolling"
	DefaultStateMode      = "stateless"
)

var appNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type Config struct {
	Name      string          `json:"name" yaml:"name"`
	Image     string          `json:"image" yaml:"image"`
	Service   ServiceConfig   `json:"service" yaml:"service"`
	Routing   RoutingConfig   `json:"routing" yaml:"routing"`
	Deploy    DeployConfig    `json:"deploy" yaml:"deploy"`
	Placement PlacementConfig `json:"placement" yaml:"placement"`
	Secrets   []string        `json:"secrets,omitempty" yaml:"secrets"`
	State     StateConfig     `json:"state" yaml:"state"`
}

type ServiceConfig struct {
	Port   int          `json:"port" yaml:"port"`
	Health HealthConfig `json:"health" yaml:"health"`
}

type HealthConfig struct {
	Path string `json:"path" yaml:"path"`
}

type RoutingConfig struct {
	Domain string `json:"domain" yaml:"domain"`
}

type DeployConfig struct {
	Replicas int    `json:"replicas" yaml:"replicas"`
	Strategy string `json:"strategy" yaml:"strategy"`
}

type PlacementConfig struct {
	Arch     string              `json:"arch" yaml:"arch"`
	Spread   bool                `json:"spread" yaml:"spread"`
	Prefer   []map[string]string `json:"prefer,omitempty" yaml:"prefer"`
	Fallback []map[string]string `json:"fallback,omitempty" yaml:"fallback"`
}

type StateConfig struct {
	Mode string `json:"mode" yaml:"mode"`
}

func Parse(data []byte) (Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse deployer.yaml: %w", err)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Normalize() {
	c.Name = strings.TrimSpace(c.Name)
	c.Image = strings.TrimSpace(c.Image)
	c.Service.Health.Path = strings.TrimSpace(c.Service.Health.Path)
	c.Routing.Domain = strings.TrimSpace(c.Routing.Domain)
	c.Deploy.Strategy = strings.TrimSpace(c.Deploy.Strategy)
	c.Placement.Arch = strings.TrimSpace(c.Placement.Arch)
	c.State.Mode = strings.TrimSpace(c.State.Mode)

	if c.Deploy.Strategy == "" {
		c.Deploy.Strategy = DefaultDeployStrategy
	}
	if c.Placement.Arch == "" {
		c.Placement.Arch = DefaultPlacementArch
	}
	if c.State.Mode == "" {
		c.State.Mode = DefaultStateMode
	}

	c.Secrets = normalizeStringList(c.Secrets)
	c.Placement.Prefer = normalizePlacementSelectors(c.Placement.Prefer)
	c.Placement.Fallback = normalizePlacementSelectors(c.Placement.Fallback)
}

func (c Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(c.Name) > 63 || !appNamePattern.MatchString(c.Name) {
		return fmt.Errorf("name must be a DNS-safe Kubernetes name")
	}
	if c.Image == "" {
		return fmt.Errorf("image is required")
	}
	if c.Service.Port == 0 {
		return fmt.Errorf("service.port is required")
	}
	if c.Service.Port < 1 || c.Service.Port > 65535 {
		return fmt.Errorf("service.port must be between 1 and 65535")
	}
	if c.Service.Health.Path == "" {
		return fmt.Errorf("service.health.path is required")
	}
	if !strings.HasPrefix(c.Service.Health.Path, "/") {
		return fmt.Errorf("service.health.path must start with /")
	}
	if c.Routing.Domain == "" {
		return fmt.Errorf("routing.domain is required")
	}
	if c.Deploy.Replicas < 1 {
		return fmt.Errorf("deploy.replicas must be at least 1")
	}
	switch c.State.Mode {
	case "stateless", "stateful", "cache":
	default:
		return fmt.Errorf("state.mode must be one of stateless, stateful, cache")
	}
	for i, secret := range c.Secrets {
		if secret == "" {
			return fmt.Errorf("secrets[%d] cannot be empty", i)
		}
	}
	return nil
}

func (c Config) JSON() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode desired state: %w", err)
	}
	return string(data), nil
}

func FromJSON(data string) (Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return Config{}, fmt.Errorf("decode desired state: %w", err)
	}
	cfg.Normalize()
	return cfg, nil
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.TrimSpace(value))
	}
	return out
}

func normalizePlacementSelectors(selectors []map[string]string) []map[string]string {
	if len(selectors) == 0 {
		return []map[string]string{}
	}
	normalized := make([]map[string]string, 0, len(selectors))
	for _, selector := range selectors {
		clean := make(map[string]string, len(selector))
		for key, value := range selector {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			clean[key] = strings.TrimSpace(value)
		}
		normalized = append(normalized, clean)
	}
	return normalized
}
