package config

import (
	"errors"
	"fmt"
	"os"
)

type ServerConfig struct {
	GRPCListenAddress string
	HTTPListenAddress string
	DatabaseURL       string
	PublicBaseURL     string
	SecretKey         string
	TokenHashKey      string
}

func LoadServer() ServerConfig {
	return ServerConfig{
		GRPCListenAddress: envOrDefault("DEPLOYER_SERVER_GRPC_ADDR", ":7443"),
		HTTPListenAddress: envOrDefault("DEPLOYER_SERVER_HTTP_ADDR", ":7080"),
		DatabaseURL:       envOrDefault("DEPLOYER_DATABASE_URL", "file:deployer.db"),
		PublicBaseURL:     os.Getenv("DEPLOYER_PUBLIC_BASE_URL"),
		SecretKey:         os.Getenv("DEPLOYER_SECRET_KEY"),
		TokenHashKey:      os.Getenv("DEPLOYER_TOKEN_HASH_KEY"),
	}
}

func (c ServerConfig) Validate() error {
	var errs []error
	if c.GRPCListenAddress == "" {
		errs = append(errs, errors.New("DEPLOYER_SERVER_GRPC_ADDR is required"))
	}
	if c.HTTPListenAddress == "" {
		errs = append(errs, errors.New("DEPLOYER_SERVER_HTTP_ADDR is required"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DEPLOYER_DATABASE_URL is required"))
	}
	if c.PublicBaseURL == "" {
		errs = append(errs, errors.New("DEPLOYER_PUBLIC_BASE_URL is required"))
	}
	if c.SecretKey == "" {
		errs = append(errs, errors.New("DEPLOYER_SECRET_KEY is required"))
	}
	if c.TokenHashKey == "" {
		errs = append(errs, errors.New("DEPLOYER_TOKEN_HASH_KEY is required"))
	}
	return errors.Join(errs...)
}

type AgentConfig struct {
	ServerURL          string
	NodeCredentialPath string
	WireGuardInterface string
}

func LoadAgent() AgentConfig {
	return AgentConfig{
		ServerURL:          os.Getenv("DEPLOYER_SERVER_URL"),
		NodeCredentialPath: envOrDefault("DEPLOYER_AGENT_CREDENTIAL_PATH", "/etc/deployer/agent/token"),
		WireGuardInterface: envOrDefault("DEPLOYER_WIREGUARD_INTERFACE", "wg0"),
	}
}

func (c AgentConfig) Validate() error {
	var errs []error
	if c.ServerURL == "" {
		errs = append(errs, errors.New("DEPLOYER_SERVER_URL is required"))
	}
	if c.NodeCredentialPath == "" {
		errs = append(errs, errors.New("DEPLOYER_AGENT_CREDENTIAL_PATH is required"))
	}
	if c.WireGuardInterface == "" {
		errs = append(errs, errors.New("DEPLOYER_WIREGUARD_INTERFACE is required"))
	}
	return errors.Join(errs...)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func FormatValidationError(component string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s config invalid: %w", component, err)
}
