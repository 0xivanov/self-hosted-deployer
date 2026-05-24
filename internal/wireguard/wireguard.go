package wireguard

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

const (
	DefaultSubnet = "10.8.0.0/24"
	DefaultHubIP  = "10.8.0.1"
	keySize       = 32
)

type KeyPair struct {
	PrivateKey string
	PublicKey  string
}

func GenerateKeyPair() (KeyPair, error) {
	return GenerateKeyPairFrom(rand.Reader)
}

func GenerateKeyPairFrom(random io.Reader) (KeyPair, error) {
	privateKey, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate WireGuard keypair: %w", err)
	}
	return KeyPair{
		PrivateKey: base64.StdEncoding.EncodeToString(privateKey.Bytes()),
		PublicKey:  base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
	}, nil
}

func ValidatePublicKey(key string) error {
	return validateKey("WireGuard public key", key)
}

func ValidatePrivateKey(key string) error {
	return validateKey("WireGuard private key", key)
}

func validateKey(name string, key string) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil {
		return fmt.Errorf("%s must be base64 encoded", name)
	}
	if len(raw) != keySize {
		return fmt.Errorf("%s must decode to %d bytes", name, keySize)
	}
	return nil
}

func NextPeerIP(subnet string, reservedIP string, existingIPs []string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return "", fmt.Errorf("parse WireGuard subnet: %w", err)
	}
	if !prefix.Addr().Is4() {
		return "", errors.New("WireGuard address allocation currently supports IPv4 subnets only")
	}
	bits := prefix.Bits()
	if bits >= 31 {
		return "", errors.New("WireGuard subnet must have at least two usable host addresses")
	}

	network := ipv4ToUint32(prefix.Masked().Addr())
	start := network + 1
	end := network + uint32(1<<(32-bits)) - 2

	reserved, err := netip.ParseAddr(strings.TrimSpace(reservedIP))
	if err != nil {
		return "", fmt.Errorf("parse reserved WireGuard IP: %w", err)
	}
	if !reserved.Is4() || !prefix.Contains(reserved) {
		return "", fmt.Errorf("reserved WireGuard IP %q is outside subnet %s", reservedIP, subnet)
	}
	used := map[netip.Addr]bool{reserved: true}
	maxUsed := ipv4ToUint32(reserved)
	for _, value := range existingIPs {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return "", fmt.Errorf("parse existing WireGuard IP %q: %w", value, err)
		}
		if !prefix.Contains(addr) {
			return "", fmt.Errorf("existing WireGuard IP %q is outside subnet %s", value, subnet)
		}
		used[addr] = true
		if current := ipv4ToUint32(addr); current > maxUsed {
			maxUsed = current
		}
	}

	next := maxUsed + 1
	if next < start {
		next = start
	}
	for current := next; current <= end; current++ {
		addr := uint32ToIPv4(current)
		if !used[addr] {
			return addr.String(), nil
		}
	}
	for current := start; current < next && current <= end; current++ {
		addr := uint32ToIPv4(current)
		if !used[addr] {
			return addr.String(), nil
		}
	}
	return "", fmt.Errorf("no WireGuard addresses available in %s", subnet)
}

func HubPeerConfig(nodes []domain.Node) (string, error) {
	peers := make([]domain.Node, 0, len(nodes))
	for _, node := range nodes {
		if strings.TrimSpace(node.WireGuardIP) == "" || strings.TrimSpace(node.WireGuardPublicKey) == "" {
			continue
		}
		if err := ValidatePublicKey(node.WireGuardPublicKey); err != nil {
			return "", fmt.Errorf("node %s has invalid WireGuard public key: %w", node.Name, err)
		}
		if _, err := netip.ParseAddr(node.WireGuardIP); err != nil {
			return "", fmt.Errorf("node %s has invalid WireGuard IP: %w", node.Name, err)
		}
		peers = append(peers, node)
	}
	sort.Slice(peers, func(i, j int) bool {
		left, _ := netip.ParseAddr(peers[i].WireGuardIP)
		right, _ := netip.ParseAddr(peers[j].WireGuardIP)
		if left == right {
			return peers[i].Name < peers[j].Name
		}
		return left.Less(right)
	})

	var builder strings.Builder
	for i, peer := range peers {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("[Peer]\n")
		builder.WriteString("# ")
		builder.WriteString(peer.Name)
		if peer.ID != "" {
			builder.WriteString(" (")
			builder.WriteString(peer.ID)
			builder.WriteString(")")
		}
		builder.WriteString("\n")
		builder.WriteString("PublicKey = ")
		builder.WriteString(strings.TrimSpace(peer.WireGuardPublicKey))
		builder.WriteString("\n")
		builder.WriteString("AllowedIPs = ")
		builder.WriteString(strings.TrimSpace(peer.WireGuardIP))
		builder.WriteString("/32\n")
	}
	return builder.String(), nil
}

func ipv4ToUint32(addr netip.Addr) uint32 {
	raw := addr.As4()
	return binary.BigEndian.Uint32(raw[:])
}

func uint32ToIPv4(value uint32) netip.Addr {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], value)
	return netip.AddrFrom4(raw)
}
