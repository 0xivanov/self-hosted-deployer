package ingress

import (
	"strings"
	"testing"
)

func TestRequireAppResourceOwnership(t *testing.T) {
	tests := []struct {
		name    string
		labels  map[string]string
		wantErr string
	}{
		{
			name:    "missing app ownership",
			labels:  map[string]string{managedByLabel: "cloudnative-pg"},
			wantErr: appOwnershipLabel,
		},
		{
			name:    "different app ownership",
			labels:  map[string]string{appOwnershipLabel: "different-app"},
			wantErr: appOwnershipLabel,
		},
		{
			name: "conflicting manager",
			labels: map[string]string{
				appOwnershipLabel: "my-api",
				managedByLabel:    "cloudnative-pg",
			},
			wantErr: managedByLabel,
		},
		{
			name:   "legacy deployer ownership",
			labels: map[string]string{appOwnershipLabel: "my-api"},
		},
		{
			name: "current deployer ownership",
			labels: map[string]string{
				appOwnershipLabel: "my-api",
				managedByLabel:    managedByDeployer,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireAppResourceOwnership("Service", "my-api", "my-api", tt.labels)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("expected ownership acceptance, got %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("expected ownership error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestManagedAppLabelsIncludeExplicitOwner(t *testing.T) {
	labels := managedAppLabels("my-api")
	if labels[appOwnershipLabel] != "my-api" || labels[managedByLabel] != managedByDeployer {
		t.Fatalf("unexpected managed app labels: %#v", labels)
	}
}
