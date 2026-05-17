package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("deployer", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	showVersion := flags.Bool("version", false, "print version information")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion {
		fmt.Println(version.Current().String())
		return 0
	}

	if flags.NArg() > 0 && flags.Arg(0) == "version" {
		fmt.Println(version.Current().String())
		return 0
	}

	if flags.NArg() > 0 && flags.Arg(0) == "help" {
		usage()
		return 0
	}

	if flags.NArg() == 0 {
		usage()
		return 0
	}

	fmt.Fprintf(os.Stderr, "unknown command %q\n", flags.Arg(0))
	usage()
	return 2
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: deployer [--version] <command>")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  version    Print version information")
	fmt.Fprintln(os.Stderr, "  help       Show help")
}
