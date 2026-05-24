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
	if cfg.K3sInstallerURL != "https://get.k3s.io" {
		t.Fatalf("unexpected installer URL %q", cfg.K3sInstallerURL)
	}
	if cfg.K3sWireGuardIP != "10.8.0.1" {
		t.Fatalf("unexpected wireguard IP %q", cfg.K3sWireGuardIP)
	}
}

func TestAgentConfigValidate(t *testing.T) {
	cfg := AgentConfig{
		ServerURL:          "https://deploy.example.com",
		NodeCredentialPath: "/tmp/token",
		WireGuardInterface: "wg0",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config: %v", err)
	}
}
