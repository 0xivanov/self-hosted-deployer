package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) secrets(args []string, opts cliOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: deployer secrets <set|list|remove>")
		return 2
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

	switch args[0] {
	case "set":
		return a.secretsSet(args[1:], resolved, client)
	case "list":
		return a.secretsList(args[1:], resolved, client)
	case "remove":
		return a.secretsRemove(args[1:], resolved, client)
	default:
		fmt.Fprintf(a.stderr, "unknown secrets command %q\n", args[0])
		fmt.Fprintln(a.stderr, "usage: deployer secrets <set|list|remove>")
		return 2
	}
}

func (a cliApp) secretsSet(args []string, opts runtimeOptions, client platformClient) int {
	flags := flag.NewFlagSet("secrets set", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	var value string
	flags.StringVar(&value, "value", "", "secret value (less safe: may be exposed in shell history)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprintln(a.stderr, "usage: deployer secrets set [--value value] <app> <name>")
		return 2
	}
	appName, name := flags.Arg(0), flags.Arg(1)
	if !flagWasSet(flags, "value") {
		var err error
		value, err = a.readSecretValue(name)
		if err != nil {
			fmt.Fprintln(a.stderr, err)
			return 1
		}
	}
	if err := client.SetSecret(context.Background(), appName, name, value); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, map[string]any{"app": appName, "name": name, "set": true}); err != nil {
			fmt.Fprintf(a.stderr, "render secret result: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(a.stdout, "Secret %s set for %s.\n", name, appName)
	return 0
}

func (a cliApp) secretsList(args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer secrets list <app>")
		return 2
	}
	names, err := client.ListSecrets(context.Background(), args[0])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, map[string]any{"app": args[0], "names": names}); err != nil {
			fmt.Fprintf(a.stderr, "render secrets: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(a.stdout, "NAME")
	for _, name := range names {
		fmt.Fprintln(a.stdout, name)
	}
	return 0
}

func (a cliApp) secretsRemove(args []string, opts runtimeOptions, client platformClient) int {
	flags := flag.NewFlagSet("secrets remove", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	var yes bool
	flags.BoolVar(&yes, "yes", false, "remove without confirmation")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprintln(a.stderr, "usage: deployer secrets remove [--yes] <app> <name>")
		return 2
	}
	appName, name := flags.Arg(0), flags.Arg(1)
	if !yes {
		fmt.Fprintf(a.stderr, "Remove secret %s from %s? [y/N]: ", name, appName)
		answer, err := bufio.NewReader(a.stdin).ReadString('\n')
		if err != nil {
			fmt.Fprintln(a.stderr)
			fmt.Fprintln(a.stderr, "secret removal cancelled")
			return 1
		}
		if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
			fmt.Fprintln(a.stderr, "Secret removal cancelled.")
			return 0
		}
	}
	if err := client.DeleteSecret(context.Background(), appName, name); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, map[string]any{"app": appName, "name": name, "removed": true}); err != nil {
			fmt.Fprintf(a.stderr, "render secret result: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(a.stdout, "Secret %s removed from %s.\n", name, appName)
	return 0
}
