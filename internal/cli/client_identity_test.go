package cli

import (
	"context"
	"net"
	"testing"

	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type identityPlatformServer struct {
	deployerv1.UnimplementedPlatformServiceServer
	identity string
}

func (s identityPlatformServer) GetStatus(context.Context, *deployerv1.GetStatusRequest) (*deployerv1.GetStatusResponse, error) {
	return &deployerv1.GetStatusResponse{Ready: true, ServerIdentity: s.identity}, nil
}

type countingNodeServer struct {
	deployerv1.UnimplementedNodeServiceServer
	mutations int
}

func (s *countingNodeServer) CreateJoinToken(context.Context, *deployerv1.CreateJoinTokenRequest) (*deployerv1.CreateJoinTokenResponse, error) {
	s.mutations++
	return &deployerv1.CreateJoinTokenResponse{}, nil
}

func TestVerifyServerIdentityMismatchBlocksMutation(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	nodes := &countingNodeServer{}
	deployerv1.RegisterPlatformServiceServer(grpcServer, identityPlatformServer{identity: "server-a"})
	deployerv1.RegisterNodeServiceServer(grpcServer, nodes)
	go grpcServer.Serve(listener)
	t.Cleanup(func() { grpcServer.Stop(); listener.Close() })
	conn, err := grpc.NewClient("passthrough:///bufconn", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client := NewPlatformClientForServices(deployerv1.NewPlatformServiceClient(conn), deployerv1.NewNodeServiceClient(conn), nil, "")
	if err := client.VerifyServerIdentity(context.Background(), "server-b"); err == nil {
		t.Fatal("expected identity mismatch")
	}
	if nodes.mutations != 0 {
		t.Fatalf("mutation count = %d, want 0", nodes.mutations)
	}
}

func TestVerifyServerIdentityOldServerFailsClosed(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	deployerv1.RegisterPlatformServiceServer(grpcServer, identityPlatformServer{})
	go grpcServer.Serve(listener)
	t.Cleanup(func() { grpcServer.Stop(); listener.Close() })
	conn, err := grpc.NewClient("passthrough:///bufconn", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client := NewPlatformClientForServices(deployerv1.NewPlatformServiceClient(conn), nil, nil, "")
	if err := client.VerifyServerIdentity(context.Background(), "bound-context"); err == nil {
		t.Fatal("expected old server to fail closed for bound context")
	}
}
