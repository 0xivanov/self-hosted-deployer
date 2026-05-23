package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) login(args []string, opts cliOptions) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer login <server-url>")
		return 2
	}

	serverURL, err := clicore.NormalizeServerURL(args[0])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}

	token := strings.TrimSpace(opts.token)
	if token == "" {
		fmt.Fprint(a.stderr, "Admin token: ")
		line, err := readLine(a.stdin)
		if err != nil {
			fmt.Fprintf(a.stderr, "read admin token: %v\n", err)
			return 1
		}
		token = strings.TrimSpace(line)
	}
	if token == "" {
		fmt.Fprintln(a.stderr, "admin token is required")
		return 2
	}

	client, closeClient, err := a.newPlatformClient(serverURL, token)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	defer closeClient()

	if _, err := client.Status(context.Background()); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}

	output, err := resolveLoginOutput(opts)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}

	cfg := clicore.Config{
		ServerURL:  serverURL,
		AdminToken: token,
		Output:     output,
	}
	if err := clicore.SaveConfig(opts.configPath, cfg); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}

	fmt.Fprintf(a.stdout, "logged in to %s\n", serverURL)
	return 0
}

func resolveLoginOutput(opts cliOptions) (string, error) {
	if opts.outputSet {
		return opts.output, nil
	}

	cfg, err := clicore.LoadConfig(opts.configPath)
	if errors.Is(err, clicore.ErrConfigNotFound) {
		return opts.output, nil
	}
	if err != nil {
		return "", err
	}
	return cfg.Output, nil
}
