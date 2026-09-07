package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

type cliOptions struct {
	serverURL     string
	serverSet     bool
	token         string
	tokenSet      bool
	configPath    string
	context       string
	environmentID string
	customerLabel string
	output        string
	outputSet     bool
	showVersion   bool
}

func (o cliOptions) validate() error {
	return clicore.ValidateOutputFormat(o.output)
}

type runtimeOptions struct {
	serverURL     string
	token         string
	output        string
	context       string
	environmentID string
	customerLabel string
}

func resolveRuntimeOptions(opts cliOptions) (runtimeOptions, error) {
	cfg, err := clicore.LoadConfig(opts.configPath)
	if err != nil && !errors.Is(err, clicore.ErrConfigNotFound) {
		return runtimeOptions{}, err
	}

	resolved := runtimeOptions{serverURL: cfg.ServerURL, token: cfg.AdminToken, output: cfg.Output}
	if resolved.output == "" {
		resolved.output = clicore.OutputHuman
	}
	selectedContext := strings.TrimSpace(opts.context)
	if selectedContext == "" {
		selectedContext = strings.TrimSpace(os.Getenv("DEPLOYER_CONTEXT"))
	}
	if selectedContext == "" {
		selectedContext = strings.TrimSpace(cfg.CurrentContext)
	}
	contextSelected := selectedContext != ""
	if contextSelected {
		ctx, contextErr := cfg.Context(selectedContext)
		if contextErr != nil {
			return runtimeOptions{}, contextErr
		}
		if ctx.ServerIdentity != "" {
			return runtimeOptions{}, errors.New("server identity binding is not supported by this client; do not use this context until identity verification is implemented")
		}
		resolved.context = selectedContext
		resolved.serverURL = ctx.ServerURL
		resolved.token, contextErr = clicore.ResolveContextCredential(ctx)
		if contextErr != nil {
			return runtimeOptions{}, fmt.Errorf("resolve context %q: %w", selectedContext, contextErr)
		}
		resolved.environmentID = ctx.EnvironmentID
		resolved.customerLabel = ctx.CustomerLabel
	}
	if contextSelected && (opts.serverSet || opts.serverURL != "") {
		return runtimeOptions{}, errors.New("cannot override a named context endpoint; select another context instead")
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
