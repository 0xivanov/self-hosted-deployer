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
	requestID := flags.String("request-id", "", "durable deployment request ID (64 lowercase hexadecimal characters)")
	reportWithdrawal := flags.Bool("report-withdrawal", false, "return a confirmed withdrawal result when deployment apply fails")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "usage: deployer deploy [--file path|-f path] [--dry-run] [--report-withdrawal]")
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
	client, closeClient, err := a.newVerifiedPlatformClient(resolved)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	defer closeClient()

	announceMutationTarget(a.stderr, resolved)
	var result clicore.DeployResult
	if *requestID != "" {
		tracked, ok := client.(trackedDeploymentClient)
		if !ok {
			fmt.Fprintln(a.stderr, "client does not support tracked deployments")
			return 1
		}
		result, err = tracked.DeployAppTracked(context.Background(), string(data), *requestID)
	} else if *reportWithdrawal {
		reporter, ok := client.(withdrawalReportingClient)
		if !ok {
			fmt.Fprintln(a.stderr, "client does not support withdrawal reporting")
			return 1
		}
		result, err = reporter.DeployAppReportingWithdrawal(context.Background(), string(data))
	} else {
		result, err = client.DeployApp(context.Background(), string(data))
	}
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
	if result.WithdrawalConfirmed {
		fmt.Fprintln(a.stderr, "Deployment failed. The candidate was withdrawn; verify the previous application is healthy before retrying.")
		return 1
	}
	if result.Deployment.Status == "pending" && *requestID != "" {
		fmt.Fprintln(a.stdout, "Deployment accepted and waiting for readiness.")
		fmt.Fprintf(a.stdout, "Advance: deployer apps advance %s %s\n", cfg.Name, *requestID)
		fmt.Fprintf(a.stdout, "Status: deployer apps request %s %s\n", cfg.Name, *requestID)
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
