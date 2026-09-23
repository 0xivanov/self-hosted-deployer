package main

import (
	"context"
	"fmt"

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
