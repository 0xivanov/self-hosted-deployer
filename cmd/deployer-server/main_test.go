package main

import "testing"

func TestValidateConfigUsesRuntimeRequirements(t *testing.T) {
	t.Setenv("DEPLOYER_PUBLIC_BASE_URL", "")
	t.Setenv("DEPLOYER_SECRET_KEY", "")
	t.Setenv("DEPLOYER_TOKEN_HASH_KEY", "hash-key")
	t.Setenv("DEPLOYER_DATABASE_URL", "file:deployer.db")
	t.Setenv("DEPLOYER_SERVER_GRPC_ADDR", ":7443")
	t.Setenv("DEPLOYER_SERVER_HTTP_ADDR", ":7080")

	if code := run([]string{"--validate-config"}); code != 0 {
		t.Fatalf("expected validate-config to pass, got exit code %d", code)
	}
}
