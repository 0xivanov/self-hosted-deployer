package wireguard

import (
	"strconv"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

const validPublicKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="

func TestNextPeerIPAllocatesSequentialAddresses(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		want     string
	}{
		{
			name: "first worker skips hub",
			want: "10.8.0.2",
		},
		{
			name:     "next worker follows existing allocation",
			existing: []string{"10.8.0.2", "10.8.0.3"},
			want:     "10.8.0.4",
		},
		{
			name:     "does not reuse gaps",
			existing: []string{"10.8.0.2", "10.8.0.4"},
			want:     "10.8.0.5",
		},
		{
			name:     "reuses lower address before exhaustion",
			existing: usedWireGuardIPsExcept("10.8.0.3"),
			want:     "10.8.0.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextPeerIP(DefaultSubnet, DefaultHubIP, tt.existing)
			if err != nil {
				t.Fatalf("allocate ip: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func usedWireGuardIPsExcept(except string) []string {
	ips := make([]string, 0, 252)
	for i := 2; i <= 254; i++ {
		ip := "10.8.0." + strconv.Itoa(i)
		if ip == except {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}

func TestNextPeerIPRejectsAddressesOutsideSubnet(t *testing.T) {
	_, err := NextPeerIP(DefaultSubnet, DefaultHubIP, []string{"10.9.0.2"})
	if err == nil || !strings.Contains(err.Error(), "outside subnet") {
		t.Fatalf("expected outside subnet error, got %v", err)
	}
}

func TestGenerateKeyPairProducesWireGuardSizedKeys(t *testing.T) {
	pair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	if err := ValidatePrivateKey(pair.PrivateKey); err != nil {
		t.Fatalf("invalid private key: %v", err)
	}
	if err := ValidatePublicKey(pair.PublicKey); err != nil {
		t.Fatalf("invalid public key: %v", err)
	}
	if pair.PrivateKey == pair.PublicKey {
		t.Fatal("private and public keys should differ")
	}
}

func TestHubPeerConfigIsDeterministic(t *testing.T) {
	config, err := HubPeerConfig([]domain.Node{
		{
			ID:                 "node-2",
			Name:               "pi-office",
			WireGuardIP:        "10.8.0.3",
			WireGuardPublicKey: validPublicKey,
		},
		{
			ID:                 "node-1",
			Name:               "pi-kitchen",
			WireGuardIP:        "10.8.0.2",
			WireGuardPublicKey: validPublicKey,
		},
		{
			ID:          "node-pending",
			Name:        "pi-pending",
			WireGuardIP: "10.8.0.4",
		},
		{
			ID:                 "node-removed",
			Name:               "pi-removed",
			Status:             "removed",
			WireGuardIP:        "10.8.0.5",
			WireGuardPublicKey: validPublicKey,
		},
	})
	if err != nil {
		t.Fatalf("render hub peer config: %v", err)
	}

	want := `[Peer]
# pi-kitchen (node-1)
PublicKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
AllowedIPs = 10.8.0.2/32

[Peer]
# pi-office (node-2)
PublicKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
AllowedIPs = 10.8.0.3/32
`
	if config != want {
		t.Fatalf("unexpected peer config:\n%s", config)
	}
}
