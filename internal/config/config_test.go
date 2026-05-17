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
