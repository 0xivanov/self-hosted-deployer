package main

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: deployer-server [--version] [--validate-config] [bootstrap|help]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  bootstrap  Create an initial admin token and print it once")
	fmt.Fprintln(os.Stderr, "  version    Print version information")
	fmt.Fprintln(os.Stderr, "  help       Show help")
}
