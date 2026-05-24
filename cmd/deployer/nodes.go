package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) nodes(args []string, opts cliOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: deployer nodes <add|list|inspect>")
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
	case "add":
		return a.nodesAdd(args[1:], resolved, client)
	case "list":
		return a.nodesList(args[1:], resolved, client)
	case "inspect":
		return a.nodesInspect(args[1:], resolved, client)
	default:
		fmt.Fprintf(a.stderr, "unknown nodes command %q\n", args[0])
		fmt.Fprintln(a.stderr, "usage: deployer nodes <add|list|inspect>")
		return 2
	}
}

func (a cliApp) nodesAdd(args []string, opts runtimeOptions, client platformClient) int {
	flags := flag.NewFlagSet("deployer nodes add", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	location := flags.String("location", "", "node location label")
	arch := flags.String("arch", "", "node architecture label")
	role := flags.String("role", "", "node role label")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer nodes add [--location value] [--arch value] [--role value] <name>")
		return 2
	}

	labels := map[string]string{}
	if *location != "" {
		labels["location"] = *location
	}
	if *arch != "" {
		labels["arch"] = *arch
	}
	if *role != "" {
		labels["role"] = *role
	}

	result, err := client.CreateJoinToken(context.Background(), flags.Arg(0), labels)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	result.Command = fmt.Sprintf("deployer-agent join --server %s --token %s", opts.serverURL, result.JoinToken)

	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintf(a.stderr, "render node join token: %v\n", err)
			return 1
		}
		return 0
	}

	clicore.RenderFields(a.stdout,
		clicore.Field{Name: "Node", Value: result.NodeName},
		clicore.Field{Name: "Expires", Value: result.ExpiresAt},
		clicore.Field{Name: "Join", Value: result.Command},
	)
	return 0
}

func (a cliApp) nodesList(args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 0 {
		fmt.Fprintln(a.stderr, "usage: deployer nodes list")
		return 2
	}
	nodes, err := client.ListNodes(context.Background())
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, map[string]any{"nodes": nodes}); err != nil {
			fmt.Fprintf(a.stderr, "render nodes: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(a.stdout, "%-24s %-12s %-16s %s\n", "NAME", "STATUS", "ARCH", "LAST SEEN")
	for _, node := range nodes {
		fmt.Fprintf(a.stdout, "%-24s %-12s %-16s %s\n", node.Name, node.Status, valueOrDash(node.Arch), valueOrDash(node.LastSeenAt))
	}
	return 0
}

func (a cliApp) nodesInspect(args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer nodes inspect <name-or-id>")
		return 2
	}
	node, err := client.GetNode(context.Background(), args[0])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, node); err != nil {
			fmt.Fprintf(a.stderr, "render node: %v\n", err)
			return 1
		}
		return 0
	}
	clicore.RenderFields(a.stdout,
		clicore.Field{Name: "ID", Value: node.ID},
		clicore.Field{Name: "Name", Value: node.Name},
		clicore.Field{Name: "Status", Value: node.Status},
		clicore.Field{Name: "Hostname", Value: valueOrDash(node.Hostname)},
		clicore.Field{Name: "Arch", Value: valueOrDash(node.Arch)},
		clicore.Field{Name: "WireGuard IP", Value: valueOrDash(node.WireGuardIP)},
		clicore.Field{Name: "WireGuard public key", Value: valueOrDash(node.WireGuardPublicKey)},
		clicore.Field{Name: "OS", Value: valueOrDash(node.OS)},
		clicore.Field{Name: "Kernel", Value: valueOrDash(node.Kernel)},
		clicore.Field{Name: "Last seen", Value: valueOrDash(node.LastSeenAt)},
		clicore.Field{Name: "Labels", Value: renderLabels(node.Labels)},
	)
	return 0
}
