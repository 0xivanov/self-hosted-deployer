package ingress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
)

func TestCompatibilityRenderedKubernetesSpecs(t *testing.T) {
	cases := []struct {
		name, fixture string
		tls           bool
		secret        bool
		postgres      bool
		resilience    bool
	}{
		{"stateless", "stateless-default.yaml", false, false, false, false},
		{"resilient", "resilient.yaml", false, false, false, true},
		{"tls-secrets", "tls-secrets.yaml", true, true, false, false},
		{"postgres", "postgres.yaml", false, false, true, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "appconfig", "testdata", "compatibility", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := appconfig.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := deploymentForApp(cfg, DefaultNamespace, map[bool]string{true: "fixture-secret-revision"}[tt.secret])
			if err != nil {
				t.Fatal(err)
			}
			rendered := map[string]any{"deployment": deployment, "service": serviceForApp(cfg, DefaultNamespace)}
			if tt.resilience {
				rendered["pdb"] = podDisruptionBudgetForApp(cfg, DefaultNamespace)
			}
			if tt.secret {
				rendered["secret"], err = secretForApp(cfg, DefaultNamespace, map[string]string{"API_KEY": "fixture-api-key", "DATABASE_URL": "fixture-database-url"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if cfg.Routing.Domain != "" {
				rendered["ingress"], _, err = ManifestForApp(cfg, DefaultNamespace, TLSConfig{ACMEEmail: map[bool]string{true: "ops@example.test"}[tt.tls]})
				if err != nil {
					t.Fatal(err)
				}
			}
			if tt.postgres {
				rendered["postgres"] = postgresClusterForApp(cfg, DefaultNamespace)
			}
			data, err = json.MarshalIndent(rendered, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", "compatibility", tt.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != strings.TrimSpace(string(want)) {
				t.Fatalf("rendered Kubernetes spec changed:\n%s\nwant:\n%s", data, want)
			}
		})
	}
}
