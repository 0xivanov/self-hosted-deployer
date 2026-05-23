package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) run(args []string) int {
	opts, commandArgs, code, done := a.parseRoot(args)
	if done {
		return code
	}
	return a.dispatch(commandArgs, opts)
}

func (a cliApp) parseRoot(args []string) (cliOptions, []string, int, bool) {
	opts := cliOptions{}
	flags := rootFlags(a.stderr, &opts)
	flags.Usage = func() {
		usage(a.stderr, flags)
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, 0, true
		}
		return opts, nil, 2, true
	}

	opts.outputSet = flagWasSet(flags, "output")
	if opts.output == "" {
		opts.output = clicore.OutputHuman
	}
	if err := opts.validate(); err != nil {
		fmt.Fprintln(a.stderr, err)
		return opts, nil, 2, true
	}

	if opts.showVersion {
		return opts, nil, printVersion(a.stdout, a.stderr, opts.output), true
	}

	if flags.NArg() == 0 {
		usage(a.stderr, flags)
		return opts, nil, 0, true
	}

	return opts, flags.Args(), 0, false
}

func (a cliApp) dispatch(args []string, opts cliOptions) int {
	switch args[0] {
	case "version":
		return printVersion(a.stdout, a.stderr, opts.output)
	case "login":
		return a.login(args[1:], opts)
	case "server":
		return a.server(args[1:], opts)
	case "nodes":
		return a.nodes(args[1:], opts)
	case "help":
		usage(a.stderr, rootFlags(a.stderr, &cliOptions{}))
		return 0
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\n", args[0])
		usage(a.stderr, rootFlags(a.stderr, &cliOptions{}))
		return 2
	}
}

func rootFlags(output io.Writer, opts *cliOptions) *flag.FlagSet {
	flags := flag.NewFlagSet("deployer", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.serverURL, "server", "", "control plane server URL")
	flags.StringVar(&opts.token, "token", "", "admin bearer token")
	flags.StringVar(&opts.configPath, "config", "", "path to CLI config file")
	flags.StringVar(&opts.output, "output", "", "output format: human or json")
	flags.BoolVar(&opts.showVersion, "version", false, "print version information")
	return flags
}
