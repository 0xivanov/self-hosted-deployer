package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
)

var deployRequestIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type DeployRequestResult struct {
	AppName        string          `json:"app_name"`
	RequestID      string          `json:"request_id"`
	State          string          `json:"state"`
	RequestedState json.RawMessage `json:"requested_state"`
	Result         *DeployResult   `json:"result,omitempty"`
}

func (c *PlatformClient) DeployAppTracked(ctx context.Context, yaml, id string) (DeployResult, error) {
	if !deployRequestIDPattern.MatchString(id) {
		return DeployResult{}, fmt.Errorf("invalid deployment request ID")
	}
	return c.deployApp(ctx, yaml, true, id)
}

func (c *PlatformClient) GetDeployRequest(ctx context.Context, app, id string) (DeployRequestResult, error) {
	if !deployRequestIDPattern.MatchString(id) {
		return DeployRequestResult{}, fmt.Errorf("invalid deployment request ID")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	response, err := c.appClient.GetDeployRequest(c.withBearer(ctx), &deployerv1.GetDeployRequestRequest{AppName: app, RequestId: id})
	if err != nil {
		return DeployRequestResult{}, DecodeRPCError(err)
	}
	return decodeDeployRequestMetadata(response, app, id)
}

func (c *PlatformClient) AdvanceDeployRequest(ctx context.Context, app, id string) (DeployRequestResult, error) {
	return c.mutateDeployRequest(ctx, app, id, c.appClient.AdvanceDeployRequest)
}

func (c *PlatformClient) RecoverDeployRequest(ctx context.Context, app, id string) (DeployRequestResult, error) {
	return c.mutateDeployRequest(ctx, app, id, c.appClient.RecoverDeployRequest)
}

func (c *PlatformClient) mutateDeployRequest(ctx context.Context, app, id string, call func(context.Context, *deployerv1.GetDeployRequestRequest, ...grpc.CallOption) (*deployerv1.DeployRequestMetadata, error)) (DeployRequestResult, error) {
	if !deployRequestIDPattern.MatchString(id) {
		return DeployRequestResult{}, fmt.Errorf("invalid deployment request ID")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	response, err := call(c.withBearer(ctx), &deployerv1.GetDeployRequestRequest{AppName: app, RequestId: id})
	if err != nil {
		return DeployRequestResult{}, DecodeRPCError(err)
	}
	return decodeDeployRequestMetadata(response, app, id)
}

func decodeDeployRequestMetadata(response *deployerv1.DeployRequestMetadata, app, id string) (DeployRequestResult, error) {
	if response.GetAppName() != app || response.GetRequestId() != id || !json.Valid([]byte(response.GetRequestedState())) {
		return DeployRequestResult{}, fmt.Errorf("invalid deployment request metadata")
	}
	out := DeployRequestResult{AppName: app, RequestID: id, State: response.GetState(), RequestedState: json.RawMessage(response.GetRequestedState())}
	switch out.State {
	case "pending":
		if response.GetResult() != nil {
			return DeployRequestResult{}, fmt.Errorf("pending deployment request has a result")
		}
	case "applied", "withdrawn":
		r := response.GetResult()
		if r == nil || !json.Valid([]byte(r.GetRequestedState())) {
			return DeployRequestResult{}, fmt.Errorf("deployment request result missing or invalid")
		}
		info, err := appInfo(r.GetApp())
		if err != nil {
			return DeployRequestResult{}, err
		}
		out.Result = &DeployResult{App: info, Deployment: deploymentInfo(r.GetDeployment()), WithdrawalConfirmed: r.GetWithdrawalConfirmed(), RequestedState: json.RawMessage(r.GetRequestedState())}
	default:
		return DeployRequestResult{}, fmt.Errorf("unknown deployment request state")
	}
	return out, nil
}
