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

	cfg, loadErr := clicore.LoadConfig(opts.configPath)
	if errors.Is(loadErr, clicore.ErrConfigNotFound) {
		cfg = clicore.Config{}
	} else if loadErr != nil {
		fmt.Fprintln(a.stderr, loadErr)
		return 1
	}
	cfg.Output = output
	if contextName := strings.TrimSpace(opts.context); contextName != "" {
		if cfg.Contexts == nil {
			contexts := make(map[string]clicore.Context)
			cfg.Contexts = &contexts
		}
		entry := (*cfg.Contexts)[contextName]
		entry.ServerURL = serverURL
		entry.AdminToken = token
		entry.CredentialRef = "inline"
		if opts.environmentID != "" {
			entry.EnvironmentID = opts.environmentID
		}
		if opts.customerLabel != "" {
			entry.CustomerLabel = opts.customerLabel
		}
		(*cfg.Contexts)[contextName] = entry
		cfg.CurrentContext = contextName
	} else {
		cfg.ServerURL = serverURL
		cfg.AdminToken = token
		cfg.CurrentContext = ""
	}
	if err := clicore.SaveConfig(opts.configPath, cfg); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}

	if contextName := strings.TrimSpace(opts.context); contextName != "" {
		fmt.Fprintf(a.stdout, "logged in to context %s (%s)\n", contextName, serverURL)
	} else {
		fmt.Fprintf(a.stdout, "logged in to %s\n", serverURL)
	}
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
