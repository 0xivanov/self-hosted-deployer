package wireguard

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_=+.-]{1,15}$`)

type HubCommandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	Run(ctx context.Context, name string, args ...string) error
}

type HubController struct {
	Interface string
	Runner    HubCommandRunner
}

func NewHubController(interfaceName string) HubController {
	return HubController{Interface: interfaceName, Runner: execHubRunner{}}
}

func (c HubController) SyncPeers(ctx context.Context, nodes []domain.Node) error {
	if !interfaceNamePattern.MatchString(strings.TrimSpace(c.Interface)) {
		return fmt.Errorf("invalid WireGuard interface %q", c.Interface)
	}
	if c.Runner == nil {
		return fmt.Errorf("WireGuard command runner is not configured")
	}

	desired := map[string]string{}
	keys := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Status == "removed" || strings.TrimSpace(node.WireGuardPublicKey) == "" {
			continue
		}
		if err := ValidatePublicKey(node.WireGuardPublicKey); err != nil {
			return fmt.Errorf("node %s has invalid WireGuard public key: %w", node.Name, err)
		}
		ip, err := netip.ParseAddr(strings.TrimSpace(node.WireGuardIP))
		if err != nil {
			return fmt.Errorf("node %s has invalid WireGuard IP: %w", node.Name, err)
		}
		key := strings.TrimSpace(node.WireGuardPublicKey)
		desired[key] = ip.String() + "/32"
		keys = append(keys, key)
	}
	sort.Strings(keys)

	output, err := c.Runner.Output(ctx, "wg", "show", c.Interface, "peers")
	if err != nil {
		return fmt.Errorf("inspect WireGuard hub peers: %w", err)
	}
	existing := strings.Fields(string(output))
	sort.Strings(existing)
	for _, key := range existing {
		if _, ok := desired[key]; ok {
			continue
		}
		if err := c.Runner.Run(ctx, "wg", "set", c.Interface, "peer", key, "remove"); err != nil {
			return fmt.Errorf("remove WireGuard hub peer: %w", err)
		}
	}
	for _, key := range keys {
		if err := c.Runner.Run(ctx, "wg", "set", c.Interface, "peer", key, "allowed-ips", desired[key]); err != nil {
			return fmt.Errorf("apply WireGuard hub peer: %w", err)
		}
	}
	return nil
}

type execHubRunner struct{}

func (execHubRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (execHubRunner) Run(ctx context.Context, name string, args ...string) error {
	_, err := execHubRunner{}.Output(ctx, name, args...)
	return err
}
