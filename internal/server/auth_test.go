package server

import (
	"context"
	"errors"
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
	auth := NewAuthenticator(repos.Auth(), "hash-key")
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

	auth := NewAuthenticator(repos.Auth(), "hash-key")
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

func TestAuthenticatorFailsWhenAdminTokenUsageCannotBeRecorded(t *testing.T) {
	ctx := context.Background()
	rawToken, tokenHash := createToken(t, security.AdminTokenPrefix, "hash-key")
	auth := NewAuthenticator(TokenRepositories{
		AdminTokens: failingAdminUsageRepository{
			token: domain.AdminToken{
				TokenHash: tokenHash,
				Name:      "test",
				CreatedAt: time.Now().UTC(),
			},
		},
	}, "hash-key")

	_, err := auth.UnaryInterceptor()(withBearer(ctx, rawToken), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.PlatformService/GetStatus",
	}, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
}

func TestAuthenticatorAcceptsCaseInsensitiveBearerScheme(t *testing.T) {
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

	auth := NewAuthenticator(repos.Auth(), "hash-key")
	_, err := auth.UnaryInterceptor()(withAuthorization(ctx, "bearer "+rawToken), nil, &grpc.UnaryServerInfo{
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
}

func TestAuthenticatorRejectsMalformedBearerToken(t *testing.T) {
	auth := NewAuthenticator(newTestTokenRepositories(openTestDB(t)).Auth(), "hash-key")
	_, err := auth.UnaryInterceptor()(withAuthorization(context.Background(), "Bearer"), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.PlatformService/GetStatus",
	}, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})

	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
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

	auth := NewAuthenticator(repos.Auth(), "hash-key")
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

	auth := NewAuthenticator(repos.Auth(), "hash-key")
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

func TestAuthenticatorAllowsAgentTokenForWorkerBootstrap(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	rawToken, tokenHash := createToken(t, security.AgentTokenPrefix, "hash-key")
	if err := repos.AgentTokens.Create(ctx, domain.AgentToken{
		TokenHash: tokenHash, NodeID: "node-1", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create agent token: %v", err)
	}
	auth := NewAuthenticator(repos.Auth(), "hash-key")
	_, err := auth.UnaryInterceptor()(withBearer(ctx, rawToken), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.NodeService/GetWorkerBootstrap",
	}, func(ctx context.Context, req any) (any, error) {
		caller, ok := CallerFromContext(ctx)
		if !ok || caller.Kind != CallerAgent || caller.NodeID != "node-1" {
			t.Fatalf("unexpected worker bootstrap caller: %#v", caller)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("authenticate worker bootstrap: %v", err)
	}
}

func TestAuthenticatorTreatsJoinNodeAsPublic(t *testing.T) {
	auth := NewAuthenticator(newTestTokenRepositories(openTestDB(t)).Auth(), "hash-key")
	_, err := auth.UnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.NodeService/JoinNode",
	}, func(ctx context.Context, req any) (any, error) {
		if _, ok := CallerFromContext(ctx); ok {
			t.Fatal("expected public join RPC without caller in context")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("expected public join RPC, got %v", err)
	}
}

func TestAuthenticatorRejectsJoinTokenForNonJoinRPC(t *testing.T) {
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

	auth := NewAuthenticator(repos.Auth(), "hash-key")
	_, err := auth.UnaryInterceptor()(withBearer(ctx, rawToken), nil, &grpc.UnaryServerInfo{
		FullMethod: "/deployer.v1.PlatformService/GetStatus",
	}, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestAuthenticatorSecuresEventWatchStreams(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	rawToken, tokenHash := createToken(t, security.AdminTokenPrefix, "hash-key")
	if err := repos.AdminTokens.Create(ctx, domain.AdminToken{TokenHash: tokenHash, Name: "test", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	auth := NewAuthenticator(repos.Auth(), "hash-key")
	handler := func(_ any, stream grpc.ServerStream) error {
		caller, ok := CallerFromContext(stream.Context())
		if !ok || caller.Kind != CallerAdmin {
			t.Fatalf("expected authenticated admin stream, got %#v", caller)
		}
		return nil
	}
	info := &grpc.StreamServerInfo{FullMethod: "/deployer.v1.EventService/WatchEvents"}
	if err := auth.StreamInterceptor()(nil, testServerStream{ctx: context.Background()}, info, handler); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected missing stream auth rejection, got %v", err)
	}
	if err := auth.StreamInterceptor()(nil, testServerStream{ctx: withBearer(ctx, rawToken)}, info, handler); err != nil {
		t.Fatalf("authenticate event watch stream: %v", err)
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

type testTokenRepositories struct {
	AdminTokens *db.AdminTokenRepository
	AgentTokens *db.AgentTokenRepository
	JoinTokens  *db.JoinTokenRepository
}

func (r testTokenRepositories) Auth() TokenRepositories {
	return TokenRepositories{
		AdminTokens: r.AdminTokens,
		AgentTokens: r.AgentTokens,
		JoinTokens:  r.JoinTokens,
	}
}

func newTestTokenRepositories(database *db.Db) testTokenRepositories {
	return testTokenRepositories{
		AdminTokens: db.NewAdminTokenRepository(database),
		AgentTokens: db.NewAgentTokenRepository(database),
		JoinTokens:  db.NewJoinTokenRepository(database),
	}
}

func withBearer(ctx context.Context, token string) context.Context {
	return withAuthorization(ctx, "Bearer "+token)
}

func withAuthorization(ctx context.Context, value string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", value))
}

type failingAdminUsageRepository struct {
	token domain.AdminToken
}

type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s testServerStream) Context() context.Context {
	return s.ctx
}

func (r failingAdminUsageRepository) FindByHash(context.Context, string) (domain.AdminToken, error) {
	return r.token, nil
}

func (r failingAdminUsageRepository) MarkUsed(context.Context, string, time.Time) error {
	return errors.New("write failed")
}
