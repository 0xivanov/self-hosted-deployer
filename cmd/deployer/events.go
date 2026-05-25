package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) events(args []string, opts cliOptions) int {
	flags := flag.NewFlagSet("events", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	var filter clicore.EventFilter
	var since time.Duration
	var watch bool
	flags.StringVar(&filter.App, "app", "", "filter by app name or ID")
	flags.StringVar(&filter.Node, "node", "", "filter by node name or ID")
	flags.StringVar(&filter.Type, "type", "", "filter by event type")
	flags.StringVar(&filter.Severity, "severity", "", "filter by severity: info, warning, or error")
	flags.DurationVar(&since, "since", 0, "filter events newer than this duration, for example 1h")
	flags.IntVar(&filter.Limit, "limit", 100, "maximum number of events to list")
	flags.BoolVar(&watch, "watch", false, "watch for new matching events until interrupted")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "usage: deployer events [--app name] [--node name] [--type type] [--severity severity] [--since duration] [--watch]")
		return 2
	}
	if since < 0 {
		fmt.Fprintln(a.stderr, "--since must be a positive duration")
		return 2
	}
	if since > 0 {
		filter.Since = time.Now().UTC().Add(-since)
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

	if watch {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if resolved.output != clicore.OutputJSON {
			renderEventHeader(a.stdout)
		}
		if err := client.WatchEvents(ctx, filter, func(event clicore.EventInfo) error {
			return renderEvent(a.stdout, resolved.output, event)
		}); err != nil && ctx.Err() == nil {
			fmt.Fprintln(a.stderr, err)
			return 1
		}
		return 0
	}
	events, err := client.ListEvents(context.Background(), filter)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if resolved.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, map[string]any{"events": events}); err != nil {
			fmt.Fprintf(a.stderr, "render events: %v\n", err)
			return 1
		}
		return 0
	}
	renderEventHeader(a.stdout)
	for _, event := range events {
		renderEventRow(a.stdout, event)
	}
	return 0
}

func renderEvent(w io.Writer, output string, event clicore.EventInfo) error {
	if output == clicore.OutputJSON {
		return clicore.RenderJSON(w, event)
	}
	renderEventRow(w, event)
	return nil
}

func renderEventHeader(w io.Writer) {
	fmt.Fprintf(w, "%-30s %-9s %-28s %s\n", "TIME", "SEVERITY", "TYPE", "MESSAGE")
}

func renderEventRow(w io.Writer, event clicore.EventInfo) {
	fmt.Fprintf(w, "%-30s %-9s %-28s %s\n", event.CreatedAt, event.Severity, event.Type, event.Message)
}
