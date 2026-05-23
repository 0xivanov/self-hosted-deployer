package main

import (
	"fmt"
	"io"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

func printVersion(stdout io.Writer, stderr io.Writer, output string) int {
	current := version.Current()
	if output == clicore.OutputJSON {
		if err := clicore.RenderJSON(stdout, current); err != nil {
			fmt.Fprintf(stderr, "render version: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintln(stdout, current.String())
	return 0
}
