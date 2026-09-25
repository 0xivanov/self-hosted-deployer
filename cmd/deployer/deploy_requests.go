package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
)

type trackedDeploymentClient interface {
	DeployAppTracked(context.Context, string, string) (clicore.DeployResult, error)
	GetDeployRequest(context.Context, string, string) (clicore.DeployRequestResult, error)
}

type candidateOperationClient interface {
	AdvanceDeployRequest(context.Context, string, string) (clicore.DeployRequestResult, error)
	RecoverDeployRequest(context.Context, string, string) (clicore.DeployRequestResult, error)
}

type withdrawalRequestClient interface {
	WithdrawDeployRequest(context.Context, string, string) (clicore.DeployRequestResult, error)
}

func (a cliApp) appsRequest(args []string, opts runtimeOptions, client platformClient) int {
	return a.appsRequestAction("request", args, opts, client)
}

func (a cliApp) appsRequestAction(action string, args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 2 {
		fmt.Fprintf(a.stderr, "usage: deployer apps %s <app-name> <request-id>\n", action)
		if action == "recover" {
			fmt.Fprintln(a.stderr, "recover abandons the attempted release and restores the prior release")
		}
		return 2
	}
	var result clicore.DeployRequestResult
	var err error
	switch action {
	case "request":
		tracked, ok := client.(trackedDeploymentClient)
		if !ok {
			fmt.Fprintln(a.stderr, "client does not support deployment request lookup")
			return 1
		}
		result, err = tracked.GetDeployRequest(context.Background(), args[0], args[1])
	case "advance":
		tracked, ok := client.(candidateOperationClient)
		if !ok {
			fmt.Fprintln(a.stderr, "client does not support candidate operations")
			return 1
		}
		result, err = tracked.AdvanceDeployRequest(context.Background(), args[0], args[1])
	case "recover":
		tracked, ok := client.(candidateOperationClient)
		if !ok {
			fmt.Fprintln(a.stderr, "client does not support candidate operations")
			return 1
		}
		result, err = tracked.RecoverDeployRequest(context.Background(), args[0], args[1])
	}
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintln(a.stderr, err)
			return 1
		}
	} else {
		clicore.RenderFields(a.stdout, clicore.Field{Name: "App", Value: result.AppName}, clicore.Field{Name: "Request", Value: result.RequestID}, clicore.Field{Name: "Recorded outcome", Value: result.State})
	}
	return 0
}

func (a cliApp) appsWithdraw(args []string, opts runtimeOptions, client platformClient) int {
	flags := flag.NewFlagSet("deployer apps withdraw", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath := flags.String("file", "deployer.yaml", "path to the original deployer.yaml")
	flags.StringVar(configPath, "f", "deployer.yaml", "path to the original deployer.yaml")
	flagArgs, positional, err := normalizeWithdrawArgs(args)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}
	if err := flags.Parse(append(flagArgs, positional...)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(positional) != 2 {
		fmt.Fprintln(a.stderr, "usage: deployer apps withdraw <app-name> <request-id> [--file path|-f path]")
		return 2
	}
	data, cfg, ok := a.readDeployConfig(*configPath)
	if !ok {
		return 1
	}
	if cfg.Name != positional[0] {
		fmt.Fprintf(a.stderr, "deployment configuration app %q does not match %q\n", cfg.Name, positional[0])
		return 2
	}
	withdrawer, ok := client.(withdrawalRequestClient)
	if !ok {
		fmt.Fprintln(a.stderr, "client does not support missing-request withdrawal")
		return 1
	}
	result, err := withdrawer.WithdrawDeployRequest(context.Background(), string(data), positional[1])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintln(a.stderr, err)
			return 1
		}
		return 0
	}
	clicore.RenderFields(a.stdout, clicore.Field{Name: "App", Value: result.AppName}, clicore.Field{Name: "Request", Value: result.RequestID}, clicore.Field{Name: "Recorded outcome", Value: result.State})
	return 0
}

func normalizeWithdrawArgs(args []string) (flags []string, positional []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--file" || arg == "-f":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires a path", arg)
			}
			flags = append(flags, arg, args[i+1])
			i++
		case strings.HasPrefix(arg, "--file="):
			flags = append(flags, arg)
		case arg == "--help" || arg == "-h":
			flags = append(flags, arg)
		case strings.HasPrefix(arg, "-"):
			return nil, nil, fmt.Errorf("unknown flag %q", arg)
		default:
			positional = append(positional, arg)
		}
	}
	return flags, positional, nil
}
