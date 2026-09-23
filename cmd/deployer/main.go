package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"golang.org/x/term"
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
	readSecretValue   func(label string) (string, error)
	newPlatformClient platformClientFactory
}

type platformClient interface {
	Status(ctx context.Context) (clicore.ServerStatus, error)
	CreateJoinToken(ctx context.Context, nodeName string, labels map[string]string) (clicore.JoinTokenResult, error)
	ListNodes(ctx context.Context) ([]clicore.NodeInfo, error)
	GetNode(ctx context.Context, ref string) (clicore.NodeInfo, error)
	DrainNode(ctx context.Context, ref string) (clicore.NodeInfo, error)
	UncordonNode(ctx context.Context, ref string) (clicore.NodeInfo, error)
	RemoveNode(ctx context.Context, ref string) (clicore.NodeInfo, error)
	PurgeNode(ctx context.Context, ref string) (string, error)
	RenameNode(ctx context.Context, ref string, newName string) (clicore.NodeInfo, error)
	DeployApp(ctx context.Context, deployerYAML string) (clicore.DeployResult, error)
	DeleteApp(ctx context.Context, name string) (clicore.DeleteAppResult, error)
	ListApps(ctx context.Context) ([]clicore.AppInfo, error)
	InspectApp(ctx context.Context, name string) (clicore.AppInspectResult, error)
	GetAppStatus(ctx context.Context, name string) (clicore.AppStatusResult, error)
	StreamLogs(ctx context.Context, appName string, tailLines int32, follow bool, receive func(string) error) error
	ListRoutes(ctx context.Context) ([]clicore.RouteInfo, error)
	InspectRoute(ctx context.Context, domain string) (clicore.RouteInfo, error)
	SetSecret(ctx context.Context, appName string, name string, value string) error
	ListSecrets(ctx context.Context, appName string) ([]string, error)
	DeleteSecret(ctx context.Context, appName string, name string) error
	ListEvents(ctx context.Context, filter clicore.EventFilter) ([]clicore.EventInfo, error)
	WatchEvents(ctx context.Context, filter clicore.EventFilter, receive func(clicore.EventInfo) error) error
}

type platformClientFactory func(serverURL string, token string) (platformClient, func() error, error)

type platformIdentityVerifier interface {
	VerifyServerIdentity(context.Context, string) error
}

type registryCredentialClient interface {
	CreateRegistryCredential(context.Context, string, string, string, string, string) (clicore.RegistryCredentialInfo, error)
	ListRegistryCredentials(context.Context, string) ([]clicore.RegistryCredentialInfo, error)
}

func newCLIApp(stdin io.Reader, stdout io.Writer, stderr io.Writer) cliApp {
	return cliApp{
		stdin:             stdin,
		stdout:            stdout,
		stderr:            stderr,
		readSecretValue:   terminalSecretReader(stdin, stderr),
		newPlatformClient: newPlatformClient,
	}
}

func terminalSecretReader(stdin io.Reader, stderr io.Writer) func(string) (string, error) {
	return func(label string) (string, error) {
		file, ok := stdin.(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return "", fmt.Errorf("secure secret input requires a terminal; pass --value only when shell-history exposure is acceptable")
		}
		fmt.Fprintf(stderr, "%s: ", label)
		value, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", fmt.Errorf("read secret value: %w", err)
		}
		return string(value), nil
	}
}

func newPlatformClient(serverURL string, token string) (platformClient, func() error, error) {
	client, conn, err := clicore.NewPlatformClient(serverURL, token)
	if err != nil {
		return nil, nil, err
	}
	return client, conn.Close, nil
}

func (a cliApp) newVerifiedPlatformClient(opts runtimeOptions) (platformClient, func() error, error) {
	client, closeClient, err := a.newPlatformClient(opts.serverURL, opts.token)
	if err != nil {
		return nil, nil, err
	}
	if opts.expectedIdentity != "" {
		verifier, ok := client.(platformIdentityVerifier)
		if !ok {
			_ = closeClient()
			return nil, nil, fmt.Errorf("client cannot verify bound server identity")
		}
		if err := verifier.VerifyServerIdentity(context.Background(), opts.expectedIdentity); err != nil {
			_ = closeClient()
			return nil, nil, err
		}
	}
	return client, closeClient, nil
}
