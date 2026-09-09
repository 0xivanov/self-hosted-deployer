//go:build integration

package server

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type customerAccessNodes struct {
	deployerv1.UnimplementedNodeServiceServer
	calls atomic.Int32
}

func (s *customerAccessNodes) CreateJoinToken(context.Context, *deployerv1.CreateJoinTokenRequest) (*deployerv1.CreateJoinTokenResponse, error) {
	s.calls.Add(1)
	return &deployerv1.CreateJoinTokenResponse{}, nil
}

// Real loopback RPCs and independent SQLite token stores exercise the auth
// boundary. The mutation handler is a counter, not Kubernetes provisioning.
func TestIndependentCustomerCredentialsCannotCrossControlPlanes(t *testing.T) {
	type environment struct {
		token   string
		client  deployerv1.NodeServiceClient
		service *customerAccessNodes
	}
	var environments []environment
	for _, name := range []string{"customer-a", "customer-b"} {
		repos := newTestTokenRepositories(openTestDB(t))
		token, hash := createToken(t, security.AdminTokenPrefix, name+"-hash-key")
		if err := repos.AdminTokens.Create(context.Background(), domain.AdminToken{TokenHash: hash, Name: name, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		auth := NewAuthenticator(repos.Auth(), name+"-hash-key")
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer(grpc.UnaryInterceptor(auth.UnaryInterceptor()))
		service := &customerAccessNodes{}
		deployerv1.RegisterNodeServiceServer(server, service)
		done := make(chan struct{})
		go func() { defer close(done); _ = server.Serve(listener) }()
		t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-done })
		conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		environments = append(environments, environment{token, deployerv1.NewNodeServiceClient(conn), service})
	}
	for index, env := range environments {
		for _, tc := range []struct {
			name, token string
			want        codes.Code
		}{
			{"own", env.token, codes.OK},
			{"other-customer", environments[1-index].token, codes.Unauthenticated},
			{"missing", "", codes.Unauthenticated},
		} {
			t.Run([]string{"a/", "b/"}[index]+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if tc.token != "" {
					ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tc.token)
				}
				before := env.service.calls.Load()
				_, err := env.client.CreateJoinToken(ctx, &deployerv1.CreateJoinTokenRequest{})
				if status.Code(err) != tc.want {
					t.Fatalf("code=%s, want %s", status.Code(err), tc.want)
				}
				expected := before
				if tc.want == codes.OK {
					expected++
				}
				if env.service.calls.Load() != expected {
					t.Fatal("unexpected mutation handler execution")
				}
			})
		}
	}
}
