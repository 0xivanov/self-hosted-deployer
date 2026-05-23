package main

import (
	"context"
	"io"
	"os"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

var (
	agentStdout         = io.Writer(os.Stdout)
	agentStderr         = io.Writer(os.Stderr)
	cachedKernelVersion = detectKernelVersion()
	newAgentClient      = func(serverURL string, token string) (agentClient, func() error, error) {
		client, conn, err := clicore.NewPlatformClient(serverURL, token)
		if err != nil {
			return nil, nil, err
		}
		return client, conn.Close, nil
	}
)

type agentClient interface {
	JoinNode(ctx context.Context, joinToken string, hostname string, arch string) (clicore.JoinResult, error)
	Heartbeat(ctx context.Context, heartbeat clicore.Heartbeat) error
}

func main() {
	os.Exit(run(os.Args[1:]))
}
