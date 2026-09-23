package ingress

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func TestCandidateDependenciesRejectMutableOrMismatchedEnvironmentBeforeWrites(t *testing.T) {
	for _, mode := range []string{"legacy", "unversioned", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			cfg := hostingTestConfig(t)
			values := map[string]string{"TOKEN": "synthetic"}
			revision := ""
			switch mode {
			case "legacy":
				cfg.Secrets = []string{"TOKEN"}
			case "mismatch":
				cfg.EnvironmentRevision = strings.Repeat("a", 64)
				revision = strings.Repeat("b", 64)
			}
			client := fake.NewSimpleClientset()
			c := &Controller{namespace: DefaultNamespace, namespaces: client.CoreV1().Namespaces(), appSecrets: client.CoreV1().Secrets(DefaultNamespace)}
			if _, err := c.PrepareCandidateDependencies(context.Background(), cfg, values, revision, strings.Repeat("c", 64), nil); err == nil {
				t.Fatal("unsafe environment accepted")
			}
			if len(client.Actions()) != 0 {
				t.Fatal("rejection occurred after runtime access")
			}
		})
	}
}
