package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

func (a cliApp) contexts(args []string, opts cliOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: deployer contexts <list|use>")
		return 2
	}
	cfg, err := clicore.LoadConfig(opts.configPath)
	if err != nil {
		if errors.Is(err, clicore.ErrConfigNotFound) && args[0] == "list" {
			if opts.output == clicore.OutputJSON {
				_ = clicore.RenderJSON(a.stdout, []any{})
			} else {
				fmt.Fprintln(a.stdout, "No contexts configured.")
			}
			return 0
		}
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(a.stderr, "usage: deployer contexts list")
			return 2
		}
		return renderContexts(a, cfg, opts.output)
	case "use":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(a.stderr, "usage: deployer contexts use <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		if name == "--legacy" {
			name = ""
		} else if _, err := cfg.Context(name); err != nil {
			fmt.Fprintln(a.stderr, err)
			return 1
		}
		cfg.CurrentContext = name
		if err := clicore.SaveConfig(opts.configPath, cfg); err != nil {
			fmt.Fprintln(a.stderr, err)
			return 1
		}
		if opts.output == clicore.OutputJSON {
			if err := clicore.RenderJSON(a.stdout, map[string]string{"context": name}); err != nil {
				fmt.Fprintln(a.stderr, err)
				return 1
			}
		} else {
			fmt.Fprintf(a.stdout, "current context is now %s\n", valueOrDash(name))
		}
		return 0
	default:
		fmt.Fprintf(a.stderr, "unknown contexts command %q\n", args[0])
		return 2
	}
}

type contextListEntry struct {
	Name          string `json:"name"`
	ServerURL     string `json:"server_url"`
	EnvironmentID string `json:"environment_id,omitempty"`
	CustomerLabel string `json:"customer_label,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
	Current       bool   `json:"current"`
}

func renderContexts(a cliApp, cfg clicore.Config, output string) int {
	if cfg.Contexts == nil {
		if output == clicore.OutputJSON {
			if err := clicore.RenderJSON(a.stdout, []contextListEntry{}); err != nil {
				fmt.Fprintln(a.stderr, err)
				return 1
			}
		} else {
			fmt.Fprintln(a.stdout, "No contexts configured.")
		}
		return 0
	}
	names := make([]string, 0, len(*cfg.Contexts))
	for name := range *cfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]contextListEntry, 0, len(names))
	for _, name := range names {
		ctx := (*cfg.Contexts)[name]
		entries = append(entries, contextListEntry{Name: name, ServerURL: ctx.ServerURL, EnvironmentID: ctx.EnvironmentID, CustomerLabel: ctx.CustomerLabel, CredentialRef: contextCredentialDisplay(ctx), Current: name == cfg.CurrentContext})
	}
	if output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, entries); err != nil {
			fmt.Fprintln(a.stderr, err)
			return 1
		}
		return 0
	}
	if len(entries) == 0 {
		fmt.Fprintln(a.stdout, "No contexts configured.")
		return 0
	}
	fmt.Fprintf(a.stdout, "%-20s %-28s %-20s %s\n", "NAME", "SERVER", "CUSTOMER", "CURRENT")
	for _, entry := range entries {
		current := ""
		if entry.Current {
			current = "*"
		}
		fmt.Fprintf(a.stdout, "%-20s %-28s %-20s %s\n", entry.Name, entry.ServerURL, valueOrDash(entry.CustomerLabel), current)
	}
	return 0
}

func contextCredentialDisplay(ctx clicore.Context) string {
	if ctx.CredentialRef != "" {
		return ctx.CredentialRef
	}
	if ctx.AdminToken != "" {
		return "inline"
	}
	return "-"
}
