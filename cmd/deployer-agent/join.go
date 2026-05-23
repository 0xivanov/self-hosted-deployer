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
)

func join(args []string) int {
	cfg := config.LoadAgent()
	flags := flag.NewFlagSet("deployer-agent join", flag.ContinueOnError)
	flags.SetOutput(agentStderr)
	serverURL := flags.String("server", cfg.ServerURL, "control plane server URL")
	joinToken := flags.String("token", "", "node join token")
	credentialPath := flags.String("credential-path", cfg.NodeCredentialPath, "path to store the agent credential")
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

	hostname, _ := os.Hostname()
	result, err := client.JoinNode(context.Background(), strings.TrimSpace(*joinToken), hostname, runtime.GOOS+"/"+runtime.GOARCH)
	if err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	if err := writeCredential(*credentialPath, result.AgentToken); err != nil {
		fmt.Fprintln(agentStderr, err)
		return 1
	}
	fmt.Fprintf(agentStdout, "joined node %s (%s)\n", result.NodeName, result.NodeID)
	return 0
}
