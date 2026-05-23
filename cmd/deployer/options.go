package main

import (
	"errors"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

type cliOptions struct {
	serverURL   string
	token       string
	configPath  string
	output      string
	outputSet   bool
	showVersion bool
}

func (o cliOptions) validate() error {
	return clicore.ValidateOutputFormat(o.output)
}

type runtimeOptions struct {
	serverURL string
	token     string
	output    string
}

func resolveRuntimeOptions(opts cliOptions) (runtimeOptions, error) {
	cfg, err := clicore.LoadConfig(opts.configPath)
	if err != nil && !errors.Is(err, clicore.ErrConfigNotFound) {
		return runtimeOptions{}, err
	}

	resolved := runtimeOptions{
		serverURL: cfg.ServerURL,
		token:     cfg.AdminToken,
		output:    cfg.Output,
	}
	if resolved.output == "" {
		resolved.output = clicore.OutputHuman
	}
	if opts.serverURL != "" {
		resolved.serverURL = opts.serverURL
	}
	if opts.token != "" {
		resolved.token = opts.token
	}
	if opts.outputSet {
		resolved.output = opts.output
	}

	if resolved.serverURL == "" || resolved.token == "" {
		if err != nil {
			return runtimeOptions{}, err
		}
		return runtimeOptions{}, errors.New("server URL and admin token are required; pass --server/--token or run deployer login <server-url>")
	}
	if err := clicore.ValidateOutputFormat(resolved.output); err != nil {
		return runtimeOptions{}, err
	}
	return resolved, nil
}
