package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/logging"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

func run(args []string) int {
	flags := flag.NewFlagSet("deployer-agent", flag.ContinueOnError)
	flags.SetOutput(agentStderr)
	showVersion := flags.Bool("version", false, "print version information")
	validateConfig := flags.Bool("validate-config", false, "validate agent configuration")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion || (flags.NArg() > 0 && flags.Arg(0) == "version") {
		fmt.Fprintln(agentStdout, version.Current().String())
		return 0
	}

	if *validateConfig {
		cfg := config.LoadAgent()
		if err := config.FormatValidationError("agent", cfg.Validate()); err != nil {
			fmt.Fprintln(agentStderr, err)
			return 1
		}
		fmt.Fprintln(agentStdout, "agent config ok")
		return 0
	}

	if flags.NArg() > 0 {
		switch flags.Arg(0) {
		case "join":
			return join(flags.Args()[1:])
		case "run":
			return runLoop(flags.Args()[1:])
		case "help":
			usage()
			return 0
		}
	}

	logger := logging.New("deployer-agent", os.Getenv("DEPLOYER_LOG_LEVEL"))
	logger.Info("agent starting", "version", version.Version, "commit", version.Commit)
	usage()
	return 0
}

func usage() {
	fmt.Fprintln(agentStderr, "Usage: deployer-agent [--version] [--validate-config] [join|run|help]")
	fmt.Fprintln(agentStderr)
	fmt.Fprintln(agentStderr, "Commands:")
	fmt.Fprintln(agentStderr, "  join       Register this node with the control plane")
	fmt.Fprintln(agentStderr, "  run        Send periodic heartbeats")
	fmt.Fprintln(agentStderr, "  version    Print version information")
	fmt.Fprintln(agentStderr, "  help       Show help")
}
