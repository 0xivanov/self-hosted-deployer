package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

type preflightClient interface {
	PreflightApp(context.Context, string) (clicore.PreflightResult, error)
}

func (a cliApp) preflight(args []string, opts cliOptions) int {
	flags := flag.NewFlagSet("deployer preflight", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath := flags.String("file", "deployer.yaml", "path to deployer.yaml")
	flags.StringVar(configPath, "f", "deployer.yaml", "path to deployer.yaml")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "usage: deployer preflight [--file path|-f path]")
		return 2
	}
	data, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintf(a.stderr, "read %s: %v\n", *configPath, err)
		return 1
	}
	if _, err := appconfig.Parse(data); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
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
	preflight, ok := client.(preflightClient)
	if !ok {
		fmt.Fprintln(a.stderr, "preflight is unavailable: control plane client does not support AppService/PreflightApp")
		return 1
	}
	result, err := preflight.PreflightApp(context.Background(), string(data))
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if resolved.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintf(a.stderr, "render preflight report: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(a.stdout, "Preflight passed (read-only; no resources were changed).")
	fmt.Fprintln(a.stdout, "Desired state:")
	fmt.Fprintln(a.stdout, result.DesiredState)
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.stdout, "Warning: %s\n", strings.TrimSpace(warning))
	}
	return 0
}
