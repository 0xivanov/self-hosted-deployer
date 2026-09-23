package cli

import (
	"context"
	"encoding/json"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"strings"
	"testing"
	"time"
)

type requestLookupStub struct {
	deployerv1.AppServiceClient
	response *deployerv1.DeployRequestMetadata
	calls    int
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
}
