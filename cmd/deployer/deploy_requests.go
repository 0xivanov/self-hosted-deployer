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

func (a cliApp) appsRequest(args []string, opts runtimeOptions, client platformClient) int {
	if len(args) != 2 {
		fmt.Fprintln(a.stderr, "usage: deployer apps request <app-name> <request-id>")
		return 2
	}
	tracked, ok := client.(trackedDeploymentClient)
	if !ok {
		fmt.Fprintln(a.stderr, "client does not support deployment request lookup")
		return 1
	}
	result, err := tracked.GetDeployRequest(context.Background(), args[0], args[1])
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
