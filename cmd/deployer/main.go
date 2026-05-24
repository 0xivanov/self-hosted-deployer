package main

import (
	"context"
	"io"
	"os"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
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
	DeployApp(ctx context.Context, deployerYAML string) (clicore.DeployResult, error)
	ListApps(ctx context.Context) ([]clicore.AppInfo, error)
	InspectApp(ctx context.Context, name string) (clicore.AppInspectResult, error)
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
