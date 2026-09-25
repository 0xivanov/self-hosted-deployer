package cli

import (
	"context"
	"encoding/json"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"testing"
	"time"
)

type requestLookupStub struct {
	deployerv1.AppServiceClient
	response      *deployerv1.DeployRequestMetadata
	calls         int
	advanceCalls  int
	recoverCalls  int
	withdrawCalls int
}

func (s *requestLookupStub) AdvanceDeployRequest(_ context.Context, _ *deployerv1.GetDeployRequestRequest, _ ...grpc.CallOption) (*deployerv1.DeployRequestMetadata, error) {
	s.advanceCalls++
	return s.response, nil
}
func (s *requestLookupStub) RecoverDeployRequest(_ context.Context, _ *deployerv1.GetDeployRequestRequest, _ ...grpc.CallOption) (*deployerv1.DeployRequestMetadata, error) {
	s.recoverCalls++
	return s.response, nil
}
func (s *requestLookupStub) WithdrawDeployRequest(_ context.Context, request *deployerv1.DeployAppRequest, _ ...grpc.CallOption) (*deployerv1.DeployRequestMetadata, error) {
	s.withdrawCalls++
	if !request.GetReportWithdrawal() {
		return nil, status.Error(codes.InvalidArgument, "withdrawal flag missing")
	}
	return s.response, nil
}

func (s *requestLookupStub) GetDeployRequest(_ context.Context, _ *deployerv1.GetDeployRequestRequest, _ ...grpc.CallOption) (*deployerv1.DeployRequestMetadata, error) {
	s.calls++
	return s.response, nil
}
func TestDeployRequestLookupValidatesRecordedIdentity(t *testing.T) {
	id := strings.Repeat("a", 64)
	stub := &requestLookupStub{response: &deployerv1.DeployRequestMetadata{AppName: "site", RequestId: id, State: "pending", RequestedState: `{"name":"site"}`}}
	c := &PlatformClient{appClient: stub, timeout: time.Second}
	out, err := c.GetDeployRequest(context.Background(), "site", id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil || !strings.Contains(string(b), `"requested_state":{"name":"site"}`) {
		t.Fatalf("configuration must be JSON object: %s %v", b, err)
	}
	stub.response.RequestId = strings.Repeat("b", 64)
	if _, err = c.GetDeployRequest(context.Background(), "site", id); err == nil {
		t.Fatal("mismatched request accepted")
	}
	stub.response.RequestId = id
	stub.response.State = "applied"
	if _, err = c.GetDeployRequest(context.Background(), "site", id); err == nil {
		t.Fatal("missing terminal result accepted")
	}
	before := stub.calls
	if _, err = c.GetDeployRequest(context.Background(), "site", "bad"); err == nil || stub.calls != before {
		t.Fatal("invalid ID reached server")
	}
	stub.response.State = "pending"
	if _, err = c.AdvanceDeployRequest(context.Background(), "site", id); err != nil || stub.advanceCalls != 1 {
		t.Fatalf("advance request: calls=%d err=%v", stub.advanceCalls, err)
	}
	if _, err = c.RecoverDeployRequest(context.Background(), "site", id); err != nil || stub.recoverCalls != 1 {
		t.Fatalf("recover request: calls=%d err=%v", stub.recoverCalls, err)
	}
	if _, err = c.WithdrawDeployRequest(context.Background(), "name: site\nimage: example/site:1\nservice:\n  port: 8080\n  health:\n    path: /health\ndeploy:\n  replicas: 1\n", id); err != nil || stub.withdrawCalls != 1 {
		t.Fatalf("withdraw request: calls=%d err=%v", stub.withdrawCalls, err)
	}
	before = stub.withdrawCalls
	if _, err = c.WithdrawDeployRequest(context.Background(), "name: site\nimage: example/site:1\nservice:\n  port: 8080\n  health:\n    path: /health\ndeploy:\n  replicas: 1\n", "bad"); err == nil || stub.withdrawCalls != before {
		t.Fatal("invalid withdrawal ID reached server")
	}
}
