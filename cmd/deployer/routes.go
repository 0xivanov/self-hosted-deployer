package main

import (
	"context"
	"fmt"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) routes(args []string, opts cliOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: deployer routes <list|inspect>")
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
	case "list":
		return a.routesList(args[1:], resolved, client)
	case "inspect":
		return a.routesInspect(args[1:], resolved, client)
	default:
		fmt.Fprintf(a.stderr, "unknown routes command %q\n", args[0])
		fmt.Fprintln(a.stderr, "usage: deployer routes <list|inspect>")
		return 2
	}
}

func (a cliApp) routesList(args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 0 {
		fmt.Fprintln(a.stderr, "usage: deployer routes list")
		return 2
	}
	routes, err := client.ListRoutes(context.Background())
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, map[string]any{"routes": routes}); err != nil {
			fmt.Fprintf(a.stderr, "render routes: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(a.stdout, "%-32s %-12s %-8s %s\n", "DOMAIN", "STATUS", "TLS", "PORT")
	for _, route := range routes {
		fmt.Fprintf(a.stdout, "%-32s %-12s %-8t %d\n", route.Domain, route.Status, route.TLSEnabled, route.TargetPort)
	}
	return 0
}

func (a cliApp) routesInspect(args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer routes inspect <domain>")
		return 2
	}
	route, err := client.InspectRoute(context.Background(), args[0])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, route); err != nil {
			fmt.Fprintf(a.stderr, "render route: %v\n", err)
			return 1
		}
		return 0
	}
	clicore.RenderFields(a.stdout,
		clicore.Field{Name: "Domain", Value: route.Domain},
		clicore.Field{Name: "Status", Value: route.Status},
		clicore.Field{Name: "TLS", Value: route.TLSEnabled},
		clicore.Field{Name: "Port", Value: route.TargetPort},
		clicore.Field{Name: "App ID", Value: route.AppID},
	)
	return 0
}
