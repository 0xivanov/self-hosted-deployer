package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/security"
)

type ServerConfig struct {
	GRPCListenAddress string
	HTTPListenAddress string
	DatabaseURL       string
	PublicBaseURL     string
	SecretKey         string
	SecretKeyFile     string
	TokenHashKey      string
	TLSCertFile       string
	TLSKeyFile        string
	KubeconfigPath    string
	IngressNamespace  string
	IngressACMEEmail  string
	IngressTLSIssuer  string
	IngressACMEServer string
	K3sConfigPath     string
	K3sWireGuardIP    string
	K3sInstallerURL   string
}

func LoadServer() ServerConfig {
	return ServerConfig{
		GRPCListenAddress: envOrDefault("DEPLOYER_SERVER_GRPC_ADDR", ":7443"),
		HTTPListenAddress: envOrDefault("DEPLOYER_SERVER_HTTP_ADDR", ":7080"),
		DatabaseURL:       envOrDefault("DEPLOYER_DATABASE_URL", "file:deployer.db"),
		PublicBaseURL:     os.Getenv("DEPLOYER_PUBLIC_BASE_URL"),
		SecretKey:         os.Getenv("DEPLOYER_SECRET_KEY"),
		SecretKeyFile:     os.Getenv("DEPLOYER_SECRET_KEY_FILE"),
		TokenHashKey:      os.Getenv("DEPLOYER_TOKEN_HASH_KEY"),
		TLSCertFile:       os.Getenv("DEPLOYER_SERVER_TLS_CERT_FILE"),
		TLSKeyFile:        os.Getenv("DEPLOYER_SERVER_TLS_KEY_FILE"),
		KubeconfigPath:    envOrDefault("DEPLOYER_KUBECONFIG", "/etc/rancher/k3s/k3s.yaml"),
		IngressNamespace:  envOrDefault("DEPLOYER_INGRESS_NAMESPACE", "deployer-apps"),
		IngressACMEEmail:  os.Getenv("DEPLOYER_INGRESS_ACME_EMAIL"),
		IngressTLSIssuer:  envOrDefault("DEPLOYER_INGRESS_TLS_ISSUER", "deployer-letsencrypt"),
		IngressACMEServer: envOrDefault("DEPLOYER_INGRESS_ACME_SERVER", "https://acme-v02.api.letsencrypt.org/directory"),
		K3sConfigPath:     envOrDefault("DEPLOYER_K3S_CONFIG_PATH", "/etc/rancher/k3s/config.yaml"),
		K3sWireGuardIP:    os.Getenv("DEPLOYER_K3S_WIREGUARD_IP"),
		K3sInstallerURL:   envOrDefault("DEPLOYER_K3S_INSTALLER_URL", "https://get.k3s.io"),
	}
}

func (c ServerConfig) Validate() error {
	errs := []error{}
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
	if _, err := c.SecretEncryptionKey(); err != nil {
		errs = append(errs, err)
	}
	if c.TokenHashKey == "" {
		errs = append(errs, errors.New("DEPLOYER_TOKEN_HASH_KEY is required"))
	}
	if err := ValidateTLSFiles(c.TLSCertFile, c.TLSKeyFile); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (c ServerConfig) SecretEncryptionKey() ([]byte, error) {
	if c.SecretKey != "" && c.SecretKeyFile != "" {
		return nil, errors.New("set only one of DEPLOYER_SECRET_KEY or DEPLOYER_SECRET_KEY_FILE")
	}

	var key []byte
	switch {
	case c.SecretKey != "":
		key = []byte(c.SecretKey)
	case c.SecretKeyFile != "":
		data, err := os.ReadFile(c.SecretKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read DEPLOYER_SECRET_KEY_FILE: %w", err)
		}
		key = data
	default:
		return nil, errors.New("DEPLOYER_SECRET_KEY or DEPLOYER_SECRET_KEY_FILE is required")
	}
	if len(key) != security.SecretKeyBytes {
		return nil, fmt.Errorf("DEPLOYER_SECRET_KEY or DEPLOYER_SECRET_KEY_FILE must contain exactly %d bytes", security.SecretKeyBytes)
	}
	return append([]byte(nil), key...), nil
}

type AgentConfig struct {
	ServerURL               string
	NodeCredentialPath      string
	WireGuardInterface      string
	WireGuardPrivateKeyPath string
}

func LoadAgent() AgentConfig {
	return AgentConfig{
		ServerURL:               os.Getenv("DEPLOYER_SERVER_URL"),
		NodeCredentialPath:      envOrDefault("DEPLOYER_AGENT_CREDENTIAL_PATH", "/etc/deployer/agent/token"),
		WireGuardInterface:      envOrDefault("DEPLOYER_WIREGUARD_INTERFACE", "wg0"),
		WireGuardPrivateKeyPath: envOrDefault("DEPLOYER_WIREGUARD_PRIVATE_KEY_PATH", "/etc/deployer/wireguard/privatekey"),
	}
}

func (c AgentConfig) Validate() error {
	errs := []error{}
	if c.ServerURL == "" {
		errs = append(errs, errors.New("DEPLOYER_SERVER_URL is required"))
	}
	if c.NodeCredentialPath == "" {
		errs = append(errs, errors.New("DEPLOYER_AGENT_CREDENTIAL_PATH is required"))
	}
	if c.WireGuardInterface == "" {
		errs = append(errs, errors.New("DEPLOYER_WIREGUARD_INTERFACE is required"))
	}
	if c.WireGuardPrivateKeyPath == "" {
		errs = append(errs, errors.New("DEPLOYER_WIREGUARD_PRIVATE_KEY_PATH is required"))
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

func ValidateTLSFiles(certFile string, keyFile string) error {
	if certFile == "" && keyFile == "" {
		return nil
	}
	if certFile == "" {
		return errors.New("DEPLOYER_SERVER_TLS_CERT_FILE is required when DEPLOYER_SERVER_TLS_KEY_FILE is set")
	}
	if keyFile == "" {
		return errors.New("DEPLOYER_SERVER_TLS_KEY_FILE is required when DEPLOYER_SERVER_TLS_CERT_FILE is set")
	}
	return nil
}
