package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuthenticatorRejectsMissingToken(t *testing.T) {
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	auth := NewAuthenticator(repos, "hash-key")
	_, err := auth.UnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.PlatformService/GetStatus",
	}, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})

	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestAuthenticatorAcceptsAdminTokenAndAttachesCaller(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	rawToken, tokenHash := createToken(t, security.AdminTokenPrefix, "hash-key")
	if err := repos.AdminTokens.Create(ctx, domain.AdminToken{
		TokenHash: tokenHash,
		Name:      "test",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create admin token: %v", err)
	}

	auth := NewAuthenticator(repos, "hash-key")
	_, err := auth.UnaryInterceptor()(withBearer(ctx, rawToken), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.PlatformService/GetStatus",
	}, func(ctx context.Context, req any) (any, error) {
		caller, ok := CallerFromContext(ctx)
		if !ok {
			t.Fatal("expected caller in context")
		}
		if caller.Kind != CallerAdmin {
			t.Fatalf("expected admin caller, got %s", caller.Kind)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("authenticate admin token: %v", err)
	}

	stored, err := repos.AdminTokens.FindByHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("find admin token: %v", err)
	}
	if stored.LastUsedAt == nil {
		t.Fatal("expected last_used_at to be updated")
	}
}

func TestAuthenticatorRejectsAgentTokenForAdminRPC(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	rawToken, tokenHash := createToken(t, security.AgentTokenPrefix, "hash-key")
	if err := repos.AgentTokens.Create(ctx, domain.AgentToken{
		TokenHash: tokenHash,
		NodeID:    "node-1",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create agent token: %v", err)
	}

	auth := NewAuthenticator(repos, "hash-key")
	_, err := auth.UnaryInterceptor()(withBearer(ctx, rawToken), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.PlatformService/GetStatus",
	}, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestAuthenticatorAllowsAgentTokenForHeartbeatAndAttachesNode(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	rawToken, tokenHash := createToken(t, security.AgentTokenPrefix, "hash-key")
	if err := repos.AgentTokens.Create(ctx, domain.AgentToken{
		TokenHash: tokenHash,
		NodeID:    "node-1",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create agent token: %v", err)
	}

	auth := NewAuthenticator(repos, "hash-key")
	_, err := auth.UnaryInterceptor()(withBearer(ctx, rawToken), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.NodeService/HeartbeatNode",
	}, func(ctx context.Context, req any) (any, error) {
		caller, ok := CallerFromContext(ctx)
		if !ok {
			t.Fatal("expected caller in context")
		}
		if caller.Kind != CallerAgent {
			t.Fatalf("expected agent caller, got %s", caller.Kind)
		}
		if caller.NodeID != "node-1" {
			t.Fatalf("expected node-1, got %q", caller.NodeID)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("authenticate agent token: %v", err)
	}
}

func TestAuthenticatorAllowsActiveJoinTokenOnlyForJoinRPC(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	rawToken, tokenHash := createToken(t, security.JoinTokenPrefix, "hash-key")
	if err := repos.JoinTokens.Create(ctx, domain.JoinToken{
		TokenHash:        tokenHash,
		IntendedNodeName: "pi-1",
		LabelsJSON:       "{}",
		CreatedAt:        time.Now().UTC(),
		ExpiresAt:        time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create join token: %v", err)
	}

	auth := NewAuthenticator(repos, "hash-key")
	_, err := auth.UnaryInterceptor()(withBearer(ctx, rawToken), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.NodeService/JoinNode",
	}, func(ctx context.Context, req any) (any, error) {
		caller, ok := CallerFromContext(ctx)
		if !ok {
			t.Fatal("expected caller in context")
		}
		if caller.Kind != CallerJoin {
			t.Fatalf("expected join caller, got %s", caller.Kind)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("authenticate join token: %v", err)
	}

	_, err = auth.UnaryInterceptor()(withBearer(ctx, rawToken), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.PlatformService/GetStatus",
	}, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func createToken(t *testing.T, prefix string, hashKey string) (string, string) {
	t.Helper()
	rawToken, err := security.NewToken(prefix)
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	tokenHash, err := security.HashToken([]byte(hashKey), rawToken)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	return rawToken, tokenHash
}

func openTestDB(t *testing.T) *db.Db {
	t.Helper()
	database, err := db.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "deployer.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	return database
}

func newTestTokenRepositories(database *db.Db) TokenRepositories {
	return TokenRepositories{
		AdminTokens: db.NewAdminTokenRepository(database),
		AgentTokens: db.NewAgentTokenRepository(database),
		JoinTokens:  db.NewJoinTokenRepository(database),
	}
}

func withBearer(ctx context.Context, token string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}
