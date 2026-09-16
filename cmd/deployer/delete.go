package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) deleteApp(args []string, opts cliOptions) int {
	flags := flag.NewFlagSet("deployer delete", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	yes := flags.Bool("yes", false, "confirm deletion")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer delete --yes <name>")
		return 2
	}
	if !*yes {
		fmt.Fprintln(a.stderr, "deployer delete requires --yes")
		return 2
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

	name := flags.Arg(0)
	announceMutationTarget(a.stderr, resolved)
	result, err := client.DeleteApp(context.Background(), name)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintf(a.stderr, "render app deletion: %v\n", err)
			return 1
		}
		return 0
	}
	if result.AlreadyAbsent {
		fmt.Fprintf(a.stdout, "App %s was already absent.\n", result.Name)
	} else {
		fmt.Fprintf(a.stdout, "App %s deleted.\n", result.Name)
	}
	return 0
}
