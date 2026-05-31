package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os/exec"
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
