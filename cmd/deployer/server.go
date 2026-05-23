package main

import (
	"context"
	"fmt"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) server(args []string, opts cliOptions) int {
	if len(args) != 1 || args[0] != "status" {
		fmt.Fprintln(a.stderr, "usage: deployer server status")
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

	status, err := client.Status(context.Background())
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}

	if resolved.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, status); err != nil {
			fmt.Fprintf(a.stderr, "render status: %v\n", err)
			return 1
		}
		return 0
	}

	clicore.RenderFields(a.stdout,
		clicore.Field{Name: "Version", Value: status.Version},
		clicore.Field{Name: "Commit", Value: status.Commit},
		clicore.Field{Name: "Build date", Value: status.BuildDate},
		clicore.Field{Name: "Ready", Value: status.Ready},
	)
	return 0
}
