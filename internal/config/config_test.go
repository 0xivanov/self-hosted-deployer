package config

import "testing"

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

func TestLoadServerK3sDefaults(t *testing.T) {
	t.Setenv("DEPLOYER_KUBECONFIG", "")
	t.Setenv("DEPLOYER_INGRESS_NAMESPACE", "")
	t.Setenv("DEPLOYER_INGRESS_ACME_EMAIL", "ops@example.com")
	t.Setenv("DEPLOYER_INGRESS_TLS_ISSUER", "")
	t.Setenv("DEPLOYER_INGRESS_ACME_SERVER", "")
	t.Setenv("DEPLOYER_K3S_CONFIG_PATH", "")
	t.Setenv("DEPLOYER_K3S_WIREGUARD_IP", "10.8.0.1")
	t.Setenv("DEPLOYER_K3S_INSTALLER_URL", "")

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
