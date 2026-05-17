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

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("deployer-server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	showVersion := flags.Bool("version", false, "print version information")
	validateConfig := flags.Bool("validate-config", false, "validate server configuration")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion || (flags.NArg() > 0 && flags.Arg(0) == "version") {
		fmt.Println(version.Current().String())
		return 0
	}

	if *validateConfig {
		cfg := config.LoadServer()
		if err := config.FormatValidationError("server", cfg.Validate()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "server config ok")
		return 0
	}

	logger := logging.New("deployer-server", os.Getenv("DEPLOYER_LOG_LEVEL"))
	logger.Info("server starting", "version", version.Version, "commit", version.Commit)
	usage()
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: deployer-server [--version] [--validate-config]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  version    Print version information")
}
