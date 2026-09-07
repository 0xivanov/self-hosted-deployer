package main

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"testing"

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type identityCommandPlatform struct {
	deployerv1.UnimplementedPlatformServiceServer
	identity string
}

func (s identityCommandPlatform) GetStatus(context.Context, *deployerv1.GetStatusRequest) (*deployerv1.GetStatusResponse, error) {
	return &deployerv1.GetStatusResponse{Ready: true, ServerIdentity: s.identity}, nil
}

type identityCommandNodes struct {
	deployerv1.UnimplementedNodeServiceServer
	createCalls int
}

func (s *identityCommandNodes) CreateJoinToken(context.Context, *deployerv1.CreateJoinTokenRequest) (*deployerv1.CreateJoinTokenResponse, error) {
	s.createCalls++
	return &deployerv1.CreateJoinTokenResponse{NodeName: "node", JoinToken: "join", ExpiresAt: "later"}, nil
}

func TestBoundContextIdentityMismatchBlocksCLIAdd(t *testing.T) {
	configPath, factory, nodes := identityCommandFixture(t, "server-a")
	app := newCLIApp(bytes.NewBufferString(""), &bytes.Buffer{}, &bytes.Buffer{})
	app.newPlatformClient = factory
	if code := app.run([]string{"--config", configPath, "--context", "customer", "nodes", "add", "node"}); code == 0 {
		t.Fatal("expected identity mismatch")
	}
	if nodes.createCalls != 0 {
		t.Fatalf("CreateJoinToken calls = %d, want 0", nodes.createCalls)
	}
}

func TestUnboundContextWorksWithOldServer(t *testing.T) {
	configPath, factory, nodes := identityCommandFixture(t, "")
	app := newCLIApp(bytes.NewBufferString(""), &bytes.Buffer{}, &bytes.Buffer{})
	app.newPlatformClient = factory
	if code := app.run([]string{"--config", configPath, "--context", "customer", "nodes", "add", "node"}); code != 0 {
		t.Fatalf("expected old server compatibility, got %d", code)
	}
	if nodes.createCalls != 1 {
		t.Fatalf("CreateJoinToken calls = %d, want 1", nodes.createCalls)
	}
}

func identityCommandFixture(t *testing.T, serverIdentity string) (string, platformClientFactory, *identityCommandNodes) {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	nodes := &identityCommandNodes{}
	deployerv1.RegisterPlatformServiceServer(grpcServer, identityCommandPlatform{identity: serverIdentity})
	deployerv1.RegisterNodeServiceServer(grpcServer, nodes)
	go grpcServer.Serve(listener)
	t.Cleanup(func() { grpcServer.Stop(); listener.Close() })

	configPath := filepath.Join(t.TempDir(), "config.json")
	contexts := map[string]clicore.Context{"customer": {ServerURL: "bufconn", AdminToken: "dep_admin_test", ServerIdentity: "expected"}}
	if serverIdentity == "" {
		contexts["customer"] = clicore.Context{ServerURL: "bufconn", AdminToken: "dep_admin_test"}
	}
	if err := clicore.SaveConfig(configPath, clicore.Config{Contexts: &contexts, CurrentContext: "customer"}); err != nil {
		t.Fatal(err)
	}
	factory := func(string, string) (platformClient, func() error, error) {
		conn, err := grpc.NewClient("passthrough:///bufconn", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithInsecure())
		if err != nil {
			return nil, nil, err
		}
		return clicore.NewPlatformClientForServices(deployerv1.NewPlatformServiceClient(conn), deployerv1.NewNodeServiceClient(conn), nil, ""), conn.Close, nil
	}
	return configPath, factory, nodes
}
