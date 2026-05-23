package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return runWithIO(args, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdout io.Writer, stderr io.Writer) int {
	opts := cliOptions{}
	flags := flag.NewFlagSet("deployer", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.serverURL, "server", "", "control plane server URL")
	flags.StringVar(&opts.token, "token", "", "admin bearer token")
	flags.StringVar(&opts.configPath, "config", "", "path to CLI config file")
	flags.StringVar(&opts.output, "output", outputHuman, "output format: human or json")
	flags.BoolVar(&opts.showVersion, "version", false, "print version information")
	flags.Usage = func() {
		usage(stderr, flags)
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if err := opts.validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	if opts.showVersion {
		return printVersion(stdout, stderr, opts.output)
	}

	if flags.NArg() == 0 {
		usage(stderr, flags)
		return 0
	}

	switch flags.Arg(0) {
	case "version":
		return printVersion(stdout, stderr, opts.output)
	case "help":
		usage(stderr, flags)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", flags.Arg(0))
		usage(stderr, flags)
		return 2
	}
}

const (
	outputHuman = "human"
	outputJSON  = "json"
)

type cliOptions struct {
	serverURL   string
	token       string
	configPath  string
	output      string
	showVersion bool
}

func (o cliOptions) validate() error {
	switch o.output {
	case outputHuman, outputJSON:
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; expected human or json", o.output)
	}
}

func printVersion(stdout io.Writer, stderr io.Writer, output string) int {
	current := version.Current()
	if output == outputJSON {
		if err := json.NewEncoder(stdout).Encode(current); err != nil {
			fmt.Fprintf(stderr, "render version: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintln(stdout, current.String())
	return 0
}

func usage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: deployer [global flags] <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  version    Print version information")
	fmt.Fprintln(w, "  help       Show help")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global Flags:")
	flags.VisitAll(func(f *flag.Flag) {
		line := fmt.Sprintf("  --%s", f.Name)
		if f.DefValue != "false" {
			line += " " + f.DefValue
		}
		fmt.Fprintln(w, line)
		fmt.Fprintf(w, "\t%s\n", f.Usage)
	})
}
