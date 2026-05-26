package wireguard

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

const DefaultPersistentKeepalive = 25

type NodeConfig struct {
	PrivateKey          string
	Address             string
	HubPublicKey        string
	Endpoint            string
	AllowedIPs          string
	PersistentKeepalive int
}

func (c NodeConfig) WithDefaults() NodeConfig {
	if strings.TrimSpace(c.AllowedIPs) == "" {
		c.AllowedIPs = DefaultSubnet
	}
	if c.PersistentKeepalive <= 0 {
		c.PersistentKeepalive = DefaultPersistentKeepalive
	}
	return c
}

func RenderNodeConfig(cfg NodeConfig) (string, error) {
	cfg = cfg.WithDefaults()
	if err := ValidatePrivateKey(cfg.PrivateKey); err != nil {
		return "", err
	}
	if err := ValidatePublicKey(cfg.HubPublicKey); err != nil {
		return "", err
	}
	address, err := netip.ParseAddr(strings.TrimSpace(cfg.Address))
	if err != nil {
		return "", fmt.Errorf("parse node WireGuard IP: %w", err)
	}
	if _, err := netip.ParsePrefix(strings.TrimSpace(cfg.AllowedIPs)); err != nil {
		return "", fmt.Errorf("parse WireGuard allowed IPs: %w", err)
	}
	if _, _, err := net.SplitHostPort(strings.TrimSpace(cfg.Endpoint)); err != nil {
		return "", fmt.Errorf("parse WireGuard endpoint: %w", err)
	}

	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = %s
PersistentKeepalive = %d
`, strings.TrimSpace(cfg.PrivateKey), address.String(), strings.TrimSpace(cfg.HubPublicKey),
		strings.TrimSpace(cfg.Endpoint), strings.TrimSpace(cfg.AllowedIPs), cfg.PersistentKeepalive), nil
}
