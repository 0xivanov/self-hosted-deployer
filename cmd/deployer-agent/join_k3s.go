package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/k3s"
	"github.com/0xivanov/self-hosted-deployer/internal/wireguard"
)

var (
	newAgentK3sBootstrapper = k3s.NewBootstrapper
	runAgentCommand         = runCommand
	checkVPNConnectivity    = tcpVPNConnectivityCheck
)

func joinK3s(args []string) int {
	cfg := config.LoadAgent()
	flags := flag.NewFlagSet("deployer-agent join-k3s", flag.ContinueOnError)
	flags.SetOutput(agentStderr)
	serverURL := flags.String("server", cfg.ServerURL, "control plane server URL")
	credentialPath := flags.String("credential-path", cfg.NodeCredentialPath, "path to the agent credential")
	agentEnvPath := flags.String("agent-env-path", cfg.AgentEnvPath, "path to the agent environment file")
	privateKeyPath := flags.String("wireguard-private-key-path", cfg.WireGuardPrivateKeyPath, "path to the WireGuard private key")
	wireGuardConfigPath := flags.String("wireguard-config-path", cfg.WireGuardConfigPath, "path to write WireGuard configuration")
	wireGuardInterface := flags.String("wireguard-interface", cfg.WireGuardInterface, "WireGuard interface name")
	k3sConfigPath := flags.String("k3s-config-path", cfg.K3sConfigPath, "path to write k3s agent configuration")
	installerURL := flags.String("installer-url", cfg.K3sInstallerURL, "official k3s installer URL")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*serverURL) == "" {
		fmt.Fprintln(agentStderr, "usage: deployer-agent join-k3s --server <url>")
		return 2
	}
	token, err := readCredential(*credentialPath)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	privateKey, err := readWireGuardPrivateKey(*privateKeyPath)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	client, closeClient, err := newAgentClient(*serverURL, token)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	defer closeClient()
	material, err := client.GetWorkerBootstrap(context.Background())
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	if err := persistWireGuardHubIP(*agentEnvPath, material.WireGuardHubIP); err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	wireGuardConfig, err := wireguard.RenderNodeConfig(wireguard.NodeConfig{
		PrivateKey: privateKey, Address: material.WireGuardIP, HubPublicKey: material.WireGuardHubPublicKey,
		Endpoint: material.WireGuardEndpoint, AllowedIPs: material.WireGuardSubnet,
	})
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	if err := writeSecretFile(*wireGuardConfigPath, wireGuardConfig); err != nil {
		fmt.Fprintf(agentStderr, "write WireGuard config: %v\n", err)
		return 1
	}
	if err := ensureWireGuardUp(context.Background(), *wireGuardInterface, *wireGuardConfigPath); err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	if err := checkVPNConnectivity(context.Background(), material.WireGuardHubIP); err != nil {
		fmt.Fprintf(agentStderr, "WireGuard connectivity to hub failed: %v\n", err)
		return 1
	}
	if err := newAgentK3sBootstrapper().BootstrapWorker(context.Background(), k3s.WorkerConfig{
		ServerURL: material.K3sURL, Token: material.K3sToken, NodeName: material.NodeName,
		NodeIP: material.WireGuardIP, ConfigPath: *k3sConfigPath, InstallerURL: *installerURL,
		FlannelInterface: *wireGuardInterface,
	}); err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	fmt.Fprintf(agentStdout, "joined k3s worker %s over WireGuard\n", material.NodeName)
	return 0
}

func persistWireGuardHubIP(path string, hubIP string) error {
	hubIP = strings.TrimSpace(hubIP)
	if net.ParseIP(hubIP) == nil {
		return fmt.Errorf("persist WireGuard hub IP: invalid IP address %q", hubIP)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("persist WireGuard hub IP: environment path is empty")
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect agent environment: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("inspect agent environment: %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read agent environment: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	replaced := false
	for i, line := range lines {
		assignment := strings.TrimSpace(line)
		if strings.HasPrefix(assignment, "export ") || strings.HasPrefix(assignment, "export\t") {
			assignment = strings.TrimSpace(assignment[len("export"):])
		}
		key, _, assigned := strings.Cut(assignment, "=")
		if assigned && strings.TrimSpace(key) == "DEPLOYER_WIREGUARD_HUB_IP" {
			lines[i] = "DEPLOYER_WIREGUARD_HUB_IP=" + hubIP
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, "DEPLOYER_WIREGUARD_HUB_IP="+hubIP)
	}
	updated := strings.Join(lines, "\n")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent.env.tmp-*")
	if err != nil {
		return fmt.Errorf("create agent environment temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("restrict agent environment temporary file: %w", err)
	}
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return fmt.Errorf("write agent environment: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync agent environment: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close agent environment temporary file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace agent environment: %w", err)
	}
	return nil
}

func ensureWireGuardUp(ctx context.Context, interfaceName string, configPath string) error {
	if _, err := runAgentCommand(ctx, "wg", "show", interfaceName); err == nil {
		return nil
	}
	if _, err := runAgentCommand(ctx, "wg-quick", "up", configPath); err != nil {
		return fmt.Errorf("bring up WireGuard interface %q: %w; install wireguard-tools and verify privileges", interfaceName, err)
	}
	return nil
}

func vpnConnectivityStatus(ctx context.Context, hubIP string) string {
	if err := checkVPNConnectivity(ctx, hubIP); err != nil {
		return "disconnected"
	}
	return "connected"
}

func tcpVPNConnectivityCheck(ctx context.Context, hubIP string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(strings.TrimSpace(hubIP), "6443"))
	if err != nil {
		return err
	}
	return conn.Close()
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
