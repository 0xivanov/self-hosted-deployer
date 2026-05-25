package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

func renderLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(labels))
	for key, value := range labels {
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func readLine(r io.Reader) (string, error) {
	var builder strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return builder.String(), nil
			}
			builder.WriteByte(buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && builder.Len() > 0 {
				return builder.String(), nil
			}
			return "", err
		}
	}
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	wasSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func usage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: deployer [global flags] <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  apps       List and inspect app desired state")
	fmt.Fprintln(w, "  deploy     Submit deployer.yaml desired state")
	fmt.Fprintln(w, "  login      Save CLI access to the control plane")
	fmt.Fprintln(w, "  nodes      Add and inspect enrolled nodes")
	fmt.Fprintln(w, "  routes     List and inspect public routes")
	fmt.Fprintln(w, "  server     Inspect the configured control plane")
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
