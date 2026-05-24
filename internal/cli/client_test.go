package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestPlatformClientStatusAttachesBearerToken(t *testing.T) {
	service := &recordingPlatformService{
		status: &deployerv1.GetStatusResponse{
			Version:   "1.2.3",
			Commit:    "abc123",
			BuildDate: "2026-05-23T00:00:00Z",
			Ready:     true,
		},
	}
	client := NewPlatformClientForService(service, "dep_admin_test")

	got, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.Version != "1.2.3" || !got.Ready {
		t.Fatalf("unexpected status: %#v", got)
	}
	if service.authorization != "Bearer dep_admin_test" {
		t.Fatalf("expected bearer token metadata, got %q", service.authorization)
	}
}

func TestDecodeRPCError(t *testing.T) {
	err := DecodeRPCError(status.Error(codes.Unauthenticated, "invalid bearer token"))
	if err == nil || !strings.Contains(err.Error(), "authentication failed: invalid bearer token") {
		t.Fatalf("unexpected decoded error: %v", err)
	}

	err = DecodeRPCError(errors.New("dial failed"))
	if err == nil || !strings.Contains(err.Error(), "call control plane") {
		t.Fatalf("unexpected non-rpc error: %v", err)
	}
}

func TestNormalizeServerTarget(t *testing.T) {
	tests := map[string]string{
		"localhost:7443":          "localhost:7443",
		"http://localhost:7443":   "localhost:7443",
		"https://example.com:443": "example.com:443",
	}

	for input, want := range tests {
		got, err := NormalizeServerTarget(input)
		if err != nil {
			t.Fatalf("normalize %q: %v", input, err)
		}
		if got != want {
			t.Fatalf("normalize %q: got %q want %q", input, got, want)
		}
	}
}

func TestDialTargetAndCredentials(t *testing.T) {
	tests := map[string]struct {
		target           string
		securityProtocol string
	}{
		"localhost:7443":          {target: "localhost:7443", securityProtocol: "insecure"},
		"http://localhost:7443":   {target: "localhost:7443", securityProtocol: "insecure"},
		"https://example.com:443": {target: "example.com:443", securityProtocol: "tls"},
	}

	for input, want := range tests {
		target, transportCredentials, err := dialTargetAndCredentials(input)
		if err != nil {
			t.Fatalf("dial target %q: %v", input, err)
		}
		if target != want.target {
			t.Fatalf("dial target %q: got %q want %q", input, target, want.target)
		}
		if got := transportCredentials.Info().SecurityProtocol; got != want.securityProtocol {
			t.Fatalf("security protocol %q: got %q want %q", input, got, want.securityProtocol)
		}
	}
}

type recordingPlatformService struct {
	status        *deployerv1.GetStatusResponse
	err           error
	authorization string
}

func (s *recordingPlatformService) GetVersion(context.Context, *deployerv1.GetVersionRequest, ...grpc.CallOption) (*deployerv1.GetVersionResponse, error) {
	return &deployerv1.GetVersionResponse{}, nil
}

func (s *recordingPlatformService) GetStatus(ctx context.Context, _ *deployerv1.GetStatusRequest, _ ...grpc.CallOption) (*deployerv1.GetStatusResponse, error) {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		values := md.Get("authorization")
		if len(values) > 0 {
			s.authorization = values[0]
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.status, nil
}
