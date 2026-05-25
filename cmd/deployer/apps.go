package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) apps(args []string, opts cliOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: deployer apps <list|inspect>")
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
		return a.appsList(args[1:], resolved, client)
	case "inspect":
		return a.appsInspect(args[1:], resolved, client)
	default:
		fmt.Fprintf(a.stderr, "unknown apps command %q\n", args[0])
		fmt.Fprintln(a.stderr, "usage: deployer apps <list|inspect>")
		return 2
	}
}

func (a cliApp) appsList(args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 0 {
		fmt.Fprintln(a.stderr, "usage: deployer apps list")
		return 2
	}
	apps, err := client.ListApps(context.Background())
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, map[string]any{"apps": apps}); err != nil {
			fmt.Fprintf(a.stderr, "render apps: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(a.stdout, "%-24s %-32s %-8s %-12s %s\n", "NAME", "IMAGE", "REPLICAS", "STATE", "DOMAIN")
	for _, app := range apps {
		fmt.Fprintf(a.stdout, "%-24s %-32s %-8d %-12s %s\n", app.Name, app.Image, app.Replicas, valueOrDash(app.StateMode), valueOrDash(app.Domain))
	}
	return 0
}

func (a cliApp) appsInspect(args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer apps inspect <name>")
		return 2
	}
	result, err := client.InspectApp(context.Background(), args[0])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintf(a.stderr, "render app: %v\n", err)
			return 1
		}
		return 0
	}
	renderAppSummary(a.stdout, result.App)
	renderConfigDetails(a.stdout, result.App.DesiredState)
	if len(result.Deployments) > 0 {
		fmt.Fprintln(a.stdout)
		fmt.Fprintf(a.stdout, "%-24s %-12s %s\n", "DEPLOYMENT", "STATUS", "UPDATED")
		for _, deployment := range result.Deployments {
			fmt.Fprintf(a.stdout, "%-24s %-12s %s\n", deployment.ID, deployment.Status, deployment.UpdatedAt)
		}
	}
	if len(result.Routes) > 0 {
		fmt.Fprintln(a.stdout)
		fmt.Fprintf(a.stdout, "%-32s %-8s %-12s %s\n", "DOMAIN", "PORT", "STATUS", "TLS")
		for _, route := range result.Routes {
			fmt.Fprintf(a.stdout, "%-32s %-8d %-12s %t\n", route.Domain, route.TargetPort, route.Status, route.TLSEnabled)
		}
	}
	return 0
}

func renderAppSummary(w io.Writer, app clicore.AppInfo) {
	clicore.RenderFields(w,
		clicore.Field{Name: "Name", Value: app.Name},
		clicore.Field{Name: "Image", Value: app.Image},
		clicore.Field{Name: "Replicas", Value: app.Replicas},
		clicore.Field{Name: "Domain", Value: valueOrDash(app.Domain)},
	)
}

func renderConfigSummary(w io.Writer, cfg appconfig.Config) {
	clicore.RenderFields(w,
		clicore.Field{Name: "Name", Value: cfg.Name},
		clicore.Field{Name: "Image", Value: cfg.Image},
		clicore.Field{Name: "Replicas", Value: cfg.Deploy.Replicas},
		clicore.Field{Name: "Domain", Value: cfg.Routing.Domain},
	)
}

func renderConfigDetails(w io.Writer, cfg appconfig.Config) {
	clicore.RenderFields(w,
		clicore.Field{Name: "Port", Value: cfg.Service.Port},
		clicore.Field{Name: "Health", Value: cfg.Service.Health.Path},
		clicore.Field{Name: "Strategy", Value: cfg.Deploy.Strategy},
		clicore.Field{Name: "State", Value: cfg.State.Mode},
		clicore.Field{Name: "Arch", Value: cfg.Placement.Arch},
		clicore.Field{Name: "Spread", Value: cfg.Placement.Spread},
		clicore.Field{Name: "Prefer", Value: renderJSONish(cfg.Placement.Prefer)},
		clicore.Field{Name: "Fallback", Value: renderJSONish(cfg.Placement.Fallback)},
		clicore.Field{Name: "Secrets", Value: renderSecrets(cfg.Secrets)},
	)
}

func renderJSONish(value any) string {
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" || string(data) == "[]" {
		return "-"
	}
	return string(data)
}

func renderSecrets(secrets []string) string {
	if len(secrets) == 0 {
		return "-"
	}
	return strings.Join(secrets, ",")
}
