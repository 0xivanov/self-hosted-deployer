package main

import (
	"context"
	"fmt"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) appStatus(args []string, opts cliOptions) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer status <app>")
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
	result, err := client.GetAppStatus(context.Background(), args[0])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if resolved.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintf(a.stderr, "render app status: %v\n", err)
			return 1
		}
		return 0
	}
	route := "-"
	if len(result.Routes) > 0 {
		route = result.Routes[0].Domain
	}
	fmt.Fprintf(a.stdout, "%-18s %-28s %-9s %-9s %s\n", "APP", "IMAGE", "HEALTHY", "DESIRED", "ROUTE")
	fmt.Fprintf(a.stdout, "%-18s %-28s %-9d %-9d %s\n", result.App.Name, result.App.Image, result.AvailableReplicas, result.DesiredReplicas, route)
	fmt.Fprintln(a.stdout, "\nREPLICAS")
	for _, node := range result.RunningNodes {
		fmt.Fprintf(a.stdout, "%s\t%s\n", node, result.RuntimeStatus)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.stdout, "\nWARNING: %s\n", warning)
	}
	return 0
}
