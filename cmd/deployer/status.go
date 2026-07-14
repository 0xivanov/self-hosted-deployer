package main

import (
	"context"
	"fmt"
	"strings"

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
	routeHealth := "-"
	if len(result.Routes) > 0 {
		route = result.Routes[0].Domain
		routeHealth = valueOrDash(result.Routes[0].Status)
	}
	fmt.Fprintf(a.stdout, "%-18s %-28s %-9s %-9s %-28s %s\n", "APP", "IMAGE", "HEALTHY", "DESIRED", "ROUTE", "ROUTE HEALTH")
	fmt.Fprintf(a.stdout, "%-18s %-28s %-9d %-9d %-28s %s\n", result.App.Name, result.App.Image, result.AvailableReplicas, result.DesiredReplicas, route, routeHealth)
	fmt.Fprintln(a.stdout, "\nREPLICAS")
	for _, node := range result.RunningNodes {
		fmt.Fprintf(a.stdout, "%s\t%s\n", node, result.RuntimeStatus)
	}
	if result.Database != nil {
		databaseNodes := "-"
		if len(result.Database.RunningNodes) > 0 {
			databaseNodes = strings.Join(result.Database.RunningNodes, ", ")
		}
		fmt.Fprintln(a.stdout, "\nDATABASE")
		fmt.Fprintf(a.stdout, "STATE\t%s\n", valueOrDash(result.Database.State))
		fmt.Fprintf(a.stdout, "PHASE\t%s\n", valueOrDash(result.Database.Phase))
		fmt.Fprintf(a.stdout, "INSTANCES\t%d/%d ready\n", result.Database.ReadyInstances, result.Database.DesiredInstances)
		fmt.Fprintf(a.stdout, "PRIMARY\t%s\n", valueOrDash(result.Database.Primary))
		fmt.Fprintf(a.stdout, "NODES\t%s\n", databaseNodes)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.stdout, "\nWARNING: %s\n", warning)
	}
	return 0
}
