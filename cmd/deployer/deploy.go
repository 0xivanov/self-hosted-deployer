package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) deploy(args []string, opts cliOptions) int {
	flags := flag.NewFlagSet("deployer deploy", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath := flags.String("file", "deployer.yaml", "path to deployer.yaml")
	flags.StringVar(configPath, "f", "deployer.yaml", "path to deployer.yaml")
	dryRun := flags.Bool("dry-run", false, "validate and print desired state without server call")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "usage: deployer deploy [--file path|-f path] [--dry-run]")
		return 2
	}

	data, cfg, ok := a.readDeployConfig(*configPath)
	if !ok {
		return 1
	}

	if *dryRun {
		return a.renderDeployDryRun(opts, cfg)
	}

	resolved, err := resolveRuntimeOptions(opts)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	client, closeClient, err := a.newPlatformClient(resolved.serverURL, resolved.token)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	defer closeClient()

	result, err := client.DeployApp(context.Background(), string(data))
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if resolved.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintf(a.stderr, "render deploy result: %v\n", err)
			return 1
		}
		return 0
	}
	renderAppSummary(a.stdout, result.App)
	clicore.RenderFields(a.stdout, clicore.Field{Name: "Deployment", Value: result.Deployment.ID})
	return 0
}

func (a cliApp) readDeployConfig(path string) ([]byte, appconfig.Config, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(a.stderr, "read %s: %v\n", path, err)
		return nil, appconfig.Config{}, false
	}
	cfg, err := appconfig.Parse(data)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return nil, appconfig.Config{}, false
	}
	return data, cfg, true
}

func (a cliApp) renderDeployDryRun(opts cliOptions, cfg appconfig.Config) int {
	output := opts.output
	if output == "" {
		output = clicore.OutputHuman
	}
	if output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, cfg); err != nil {
			fmt.Fprintf(a.stderr, "render deploy dry run: %v\n", err)
			return 1
		}
		return 0
	}
	renderConfigSummary(a.stdout, cfg)
	return 0
}
