package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return newCLIApp(os.Stdin, os.Stdout, os.Stderr).run(args)
}

func runWithIO(args []string, stdout io.Writer, stderr io.Writer) int {
	return newCLIApp(strings.NewReader(""), stdout, stderr).run(args)
}

type cliApp struct {
	stdin             io.Reader
	stdout            io.Writer
	stderr            io.Writer
	newPlatformClient platformClientFactory
}

type platformClient interface {
	Status(ctx context.Context) (clicore.ServerStatus, error)
	CreateJoinToken(ctx context.Context, nodeName string, labels map[string]string) (clicore.JoinTokenResult, error)
	ListNodes(ctx context.Context) ([]clicore.NodeInfo, error)
	GetNode(ctx context.Context, ref string) (clicore.NodeInfo, error)
}

type platformClientFactory func(serverURL string, token string) (platformClient, func() error, error)

func newCLIApp(stdin io.Reader, stdout io.Writer, stderr io.Writer) cliApp {
	return cliApp{
		stdin:             stdin,
		stdout:            stdout,
		stderr:            stderr,
		newPlatformClient: newPlatformClient,
	}
}

func newPlatformClient(serverURL string, token string) (platformClient, func() error, error) {
	client, conn, err := clicore.NewPlatformClient(serverURL, token)
	if err != nil {
		return nil, nil, err
	}
	return client, conn.Close, nil
}

func (a cliApp) run(args []string) int {
	opts := cliOptions{}
	flags := flag.NewFlagSet("deployer", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	flags.StringVar(&opts.serverURL, "server", "", "control plane server URL")
	flags.StringVar(&opts.token, "token", "", "admin bearer token")
	flags.StringVar(&opts.configPath, "config", "", "path to CLI config file")
	flags.StringVar(&opts.output, "output", "", "output format: human or json")
	flags.BoolVar(&opts.showVersion, "version", false, "print version information")
	flags.Usage = func() {
		usage(a.stderr, flags)
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	opts.outputSet = flagWasSet(flags, "output")
	if opts.output == "" {
		opts.output = clicore.OutputHuman
	}
	if err := opts.validate(); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}

	if opts.showVersion {
		return printVersion(a.stdout, a.stderr, opts.output)
	}

	if flags.NArg() == 0 {
		usage(a.stderr, flags)
		return 0
	}

	switch flags.Arg(0) {
	case "version":
		return printVersion(a.stdout, a.stderr, opts.output)
	case "login":
		return a.login(flags.Args()[1:], opts)
	case "server":
		return a.server(flags.Args()[1:], opts)
	case "nodes":
		return a.nodes(flags.Args()[1:], opts)
	case "help":
		usage(a.stderr, flags)
		return 0
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\n", flags.Arg(0))
		usage(a.stderr, flags)
		return 2
	}
}

type cliOptions struct {
	serverURL   string
	token       string
	configPath  string
	output      string
	outputSet   bool
	showVersion bool
}

func (o cliOptions) validate() error {
	return clicore.ValidateOutputFormat(o.output)
}

func printVersion(stdout io.Writer, stderr io.Writer, output string) int {
	current := version.Current()
	if output == clicore.OutputJSON {
		if err := clicore.RenderJSON(stdout, current); err != nil {
			fmt.Fprintf(stderr, "render version: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintln(stdout, current.String())
	return 0
}

func (a cliApp) login(args []string, opts cliOptions) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer login <server-url>")
		return 2
	}

	serverURL, err := clicore.NormalizeServerURL(args[0])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}

	token := strings.TrimSpace(opts.token)
	if token == "" {
		fmt.Fprint(a.stderr, "Admin token: ")
		line, err := readLine(a.stdin)
		if err != nil {
			fmt.Fprintf(a.stderr, "read admin token: %v\n", err)
			return 1
		}
		token = strings.TrimSpace(line)
	}
	if token == "" {
		fmt.Fprintln(a.stderr, "admin token is required")
		return 2
	}

	client, closeClient, err := a.newPlatformClient(serverURL, token)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	defer closeClient()

	if _, err := client.Status(context.Background()); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}

	output, err := resolveLoginOutput(opts)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}

	cfg := clicore.Config{
		ServerURL:  serverURL,
		AdminToken: token,
		Output:     output,
	}
	if err := clicore.SaveConfig(opts.configPath, cfg); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}

	fmt.Fprintf(a.stdout, "logged in to %s\n", serverURL)
	return 0
}

func resolveLoginOutput(opts cliOptions) (string, error) {
	if opts.outputSet {
		return opts.output, nil
	}

	cfg, err := clicore.LoadConfig(opts.configPath)
	if errors.Is(err, clicore.ErrConfigNotFound) {
		return opts.output, nil
	}
	if err != nil {
		return "", err
	}
	return cfg.Output, nil
}

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
		clicore.Field{Name: "OS", Value: valueOrDash(node.OS)},
		clicore.Field{Name: "Kernel", Value: valueOrDash(node.Kernel)},
		clicore.Field{Name: "Last seen", Value: valueOrDash(node.LastSeenAt)},
		clicore.Field{Name: "Labels", Value: renderLabels(node.Labels)},
	)
	return 0
}

func renderLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(labels))
	for key, value := range labels {
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

type runtimeOptions struct {
	serverURL string
	token     string
	output    string
}

func resolveRuntimeOptions(opts cliOptions) (runtimeOptions, error) {
	cfg, err := clicore.LoadConfig(opts.configPath)
	if err != nil && !errors.Is(err, clicore.ErrConfigNotFound) {
		return runtimeOptions{}, err
	}

	resolved := runtimeOptions{
		serverURL: cfg.ServerURL,
		token:     cfg.AdminToken,
		output:    cfg.Output,
	}
	if resolved.output == "" {
		resolved.output = clicore.OutputHuman
	}
	if opts.serverURL != "" {
		resolved.serverURL = opts.serverURL
	}
	if opts.token != "" {
		resolved.token = opts.token
	}
	if opts.outputSet {
		resolved.output = opts.output
	}

	if resolved.serverURL == "" || resolved.token == "" {
		if err != nil {
			return runtimeOptions{}, err
		}
		return runtimeOptions{}, errors.New("server URL and admin token are required; pass --server/--token or run deployer login <server-url>")
	}
	if err := clicore.ValidateOutputFormat(resolved.output); err != nil {
		return runtimeOptions{}, err
	}
	return resolved, nil
}

func readLine(r io.Reader) (string, error) {
	var builder strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return builder.String(), nil
			}
			builder.WriteByte(buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && builder.Len() > 0 {
				return builder.String(), nil
			}
			return "", err
		}
	}
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	wasSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func usage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: deployer [global flags] <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  login      Save CLI access to the control plane")
	fmt.Fprintln(w, "  nodes      Add and inspect enrolled nodes")
	fmt.Fprintln(w, "  server     Inspect the configured control plane")
	fmt.Fprintln(w, "  version    Print version information")
	fmt.Fprintln(w, "  help       Show help")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global Flags:")
	flags.VisitAll(func(f *flag.Flag) {
		line := fmt.Sprintf("  --%s", f.Name)
		if f.DefValue != "false" {
			line += " " + f.DefValue
		}
		fmt.Fprintln(w, line)
		fmt.Fprintf(w, "\t%s\n", f.Usage)
	})
}
