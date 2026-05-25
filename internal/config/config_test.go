package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerConfigValidateRequiresSensitiveKeys(t *testing.T) {
	cfg := ServerConfig{
		GRPCListenAddress: ":7443",
		HTTPListenAddress: ":7080",
		DatabaseURL:       "file:test.db",
		PublicBaseURL:     "https://deploy.example.com",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestServerConfigSecretEncryptionKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		keyFile func(t *testing.T) string
		wantKey string
		wantErr string
	}{
		{name: "environment key", key: strings.Repeat("a", 32), wantKey: strings.Repeat("a", 32)},
		{name: "invalid environment key", key: "short", wantErr: "exactly 32 bytes"},
		{
			name:    "file key preserves trailing newline byte",
			wantKey: strings.Repeat("b", 31) + "\n",
			keyFile: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "secret.key")
				if err := os.WriteFile(path, []byte(strings.Repeat("b", 31)+"\n"), 0o600); err != nil {
					t.Fatalf("write key file: %v", err)
				}
				return path
			},
		},
		{
			name:    "file key rejects extra newline byte",
			wantErr: "exactly 32 bytes",
			keyFile: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "secret.key")
				if err := os.WriteFile(path, []byte(strings.Repeat("b", 32)+"\n"), 0o600); err != nil {
					t.Fatalf("write key file: %v", err)
				}
				return path
			},
		},
		{name: "missing key", wantErr: "is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ServerConfig{SecretKey: tt.key}
			if tt.keyFile != nil {
				cfg.SecretKeyFile = tt.keyFile(t)
			}
			key, err := cfg.SecretEncryptionKey()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected %q error, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || string(key) != tt.wantKey {
				t.Fatalf("unexpected key result %q err=%v", key, err)
			}
		})
	}
}

func TestLoadServerK3sDefaults(t *testing.T) {
	t.Setenv("DEPLOYER_KUBECONFIG", "")
	t.Setenv("DEPLOYER_INGRESS_NAMESPACE", "")
	t.Setenv("DEPLOYER_INGRESS_ACME_EMAIL", "ops@example.com")
	t.Setenv("DEPLOYER_INGRESS_TLS_ISSUER", "")
	t.Setenv("DEPLOYER_INGRESS_ACME_SERVER", "")
	t.Setenv("DEPLOYER_K3S_CONFIG_PATH", "")
	t.Setenv("DEPLOYER_K3S_WIREGUARD_IP", "10.8.0.1")
	t.Setenv("DEPLOYER_K3S_INSTALLER_URL", "")
	t.Setenv("DEPLOYER_SECRET_KEY_FILE", "/tmp/deployer.key")

	cfg := LoadServer()
	if cfg.KubeconfigPath != "/etc/rancher/k3s/k3s.yaml" {
		t.Fatalf("unexpected kubeconfig path %q", cfg.KubeconfigPath)
	}
	if cfg.K3sConfigPath != "/etc/rancher/k3s/config.yaml" {
		t.Fatalf("unexpected k3s config path %q", cfg.K3sConfigPath)
	}
	if cfg.IngressNamespace != "deployer-apps" || cfg.IngressTLSIssuer != "deployer-letsencrypt" {
		t.Fatalf("unexpected ingress defaults: %#v", cfg)
	}
	if cfg.IngressACMEEmail != "ops@example.com" || cfg.IngressACMEServer != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Fatalf("unexpected ingress TLS config: %#v", cfg)
	}
	if cfg.K3sInstallerURL != "https://get.k3s.io" {
		t.Fatalf("unexpected installer URL %q", cfg.K3sInstallerURL)
	}
	if cfg.K3sWireGuardIP != "10.8.0.1" {
		t.Fatalf("unexpected wireguard IP %q", cfg.K3sWireGuardIP)
	}
	if cfg.SecretKeyFile != "/tmp/deployer.key" {
		t.Fatalf("unexpected secret key file %q", cfg.SecretKeyFile)
	}
}

func TestServerConfigEventRetention(t *testing.T) {
	got, err := (ServerConfig{}).EventRetention()
	if err != nil {
		t.Fatalf("default event retention: %v", err)
	}
	if got.MaxAge != 720*time.Hour || got.MaxCount != 10000 || got.CleanupInterval != time.Hour {
		t.Fatalf("unexpected event retention defaults: %#v", got)
	}

	got, err = (ServerConfig{
		EventRetentionMaxAge:   "24h",
		EventRetentionMaxCount: "5",
		EventCleanupInterval:   "30m",
	}).EventRetention()
	if err != nil {
		t.Fatalf("custom event retention: %v", err)
	}
	if got.MaxAge != 24*time.Hour || got.MaxCount != 5 || got.CleanupInterval != 30*time.Minute {
		t.Fatalf("unexpected custom event retention: %#v", got)
	}

	if _, err := (ServerConfig{EventRetentionMaxCount: "0"}).EventRetention(); err == nil {
		t.Fatal("expected invalid event retention count to fail")
	}
}

func TestAgentConfigValidate(t *testing.T) {
	cfg := AgentConfig{
		ServerURL:               "https://deploy.example.com",
		NodeCredentialPath:      "/tmp/token",
		WireGuardInterface:      "wg0",
		WireGuardPrivateKeyPath: "/tmp/wg-privatekey",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config: %v", err)
	}
}

func TestLoadAgentWireGuardPrivateKeyDefault(t *testing.T) {
	t.Setenv("DEPLOYER_WIREGUARD_PRIVATE_KEY_PATH", "")

	cfg := LoadAgent()
	if cfg.WireGuardPrivateKeyPath != "/etc/deployer/wireguard/privatekey" {
		t.Fatalf("unexpected WireGuard private key path %q", cfg.WireGuardPrivateKeyPath)
	}
}
