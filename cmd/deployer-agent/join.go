package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/wireguard"
)

func join(args []string) int {
	cfg := config.LoadAgent()
	flags := flag.NewFlagSet("deployer-agent join", flag.ContinueOnError)
	flags.SetOutput(agentStderr)
	serverURL := flags.String("server", cfg.ServerURL, "control plane server URL")
	joinToken := flags.String("token", "", "node join token")
	credentialPath := flags.String("credential-path", cfg.NodeCredentialPath, "path to store the agent credential")
	wireGuardPrivateKeyPath := flags.String("wireguard-private-key-path", cfg.WireGuardPrivateKeyPath, "path to store the WireGuard private key")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(*serverURL) == "" || strings.TrimSpace(*joinToken) == "" {
		fmt.Fprintln(agentStderr, "usage: deployer-agent join --server <url> --token <join-token>")
		return 2
	}

	client, closeClient, err := newAgentClient(*serverURL, "")
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	defer closeClient()

	hostname, err := os.Hostname()
	if err != nil {
		fmt.Fprintf(agentStderr, "detect hostname: %v\n", err)
		return 1
	}
	keys, err := wireguard.GenerateKeyPair()
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	if err := writeWireGuardPrivateKey(*wireGuardPrivateKeyPath, keys.PrivateKey); err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}

	result, err := client.JoinNode(context.Background(), strings.TrimSpace(*joinToken), hostname, runtime.GOOS+"/"+runtime.GOARCH, keys.PublicKey)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	if err := writeCredential(*credentialPath, result.AgentToken); err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	fmt.Fprintf(agentStdout, "joined node %s (%s)\n", result.NodeName, result.NodeID)
	if result.WireGuardIP != "" {
		fmt.Fprintf(agentStdout, "wireguard ip: %s\n", result.WireGuardIP)
	}
	return 0
}
