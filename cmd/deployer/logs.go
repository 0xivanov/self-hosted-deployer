package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func (a cliApp) logs(args []string, opts cliOptions) int {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	var tail int
	var follow bool
	flags.IntVar(&tail, "tail", 100, "number of recent log lines to read from each pod")
	flags.BoolVar(&follow, "follow", false, "follow new log lines until interrupted")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: deployer logs <app> [--tail lines] [--follow]")
		return 2
	}
	if tail < 0 {
		fmt.Fprintln(a.stderr, "--tail cannot be negative")
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

	ctx := context.Background()
	if follow {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	err = client.StreamLogs(ctx, flags.Arg(0), int32(tail), follow, func(line string) error {
		_, err := fmt.Fprintln(a.stdout, line)
		return err
	})
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	return 0
}
