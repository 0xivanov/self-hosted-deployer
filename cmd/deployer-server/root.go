package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

func run(args []string) int {
	if err := loadDotEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

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
		if err := config.FormatValidationError("server", validateServeConfig(cfg)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "server config ok")
		return 0
	}

	if flags.NArg() > 0 {
		switch flags.Arg(0) {
		case "bootstrap":
			return bootstrap(flags.Args()[1:])
		case "help":
			usage()
			return 0
		}
	}

	return serve()
}
