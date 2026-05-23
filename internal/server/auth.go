package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Authenticator struct {
	repos   TokenRepositories
	hashKey []byte
	now     func() time.Time
}

type TokenRepositories struct {
	AdminTokens AdminTokenRepository
	AgentTokens AgentTokenRepository
	JoinTokens  JoinTokenRepository
}

type AdminTokenRepository interface {
	FindByHash(ctx context.Context, tokenHash string) (domain.AdminToken, error)
	MarkUsed(ctx context.Context, tokenHash string, usedAt time.Time) error
}

type AgentTokenRepository interface {
	FindByHash(ctx context.Context, tokenHash string) (domain.AgentToken, error)
	MarkUsed(ctx context.Context, tokenHash string, usedAt time.Time) error
}

type JoinTokenRepository interface {
	FindByHash(ctx context.Context, tokenHash string) (domain.JoinToken, error)
}

func NewAuthenticator(repos TokenRepositories, hashKey string) Authenticator {
	return Authenticator{
		repos:   repos,
		hashKey: []byte(hashKey),
		now:     time.Now,
	}
}

func (a Authenticator) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if isPublicMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		caller, err := a.authenticate(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(WithCaller(ctx, caller), req)
	}
}

func (a Authenticator) authenticate(ctx context.Context, fullMethod string) (Caller, error) {
	rawToken, err := bearerToken(ctx)
	if err != nil {
		return Caller{}, err
	}

	prefix, err := security.Prefix(rawToken)
	if err != nil {
		return Caller{}, status.Error(codes.Unauthenticated, "invalid bearer token")
	}
	tokenHash, err := security.HashToken(a.hashKey, rawToken)
	if err != nil {
		return Caller{}, status.Error(codes.Internal, "token hashing is not configured")
	}

	now := a.now().UTC()
	switch prefix {
	case security.AdminTokenPrefix:
		token, err := a.repos.AdminTokens.FindByHash(ctx, tokenHash)
		if errors.Is(err, db.ErrNotFound) {
			return Caller{}, status.Error(codes.Unauthenticated, "invalid bearer token")
		}
		if err != nil {
			return Caller{}, status.Error(codes.Internal, "authenticate admin token")
		}
		if token.RevokedAt != nil {
			return Caller{}, status.Error(codes.PermissionDenied, "admin token is revoked")
		}
		if err := a.repos.AdminTokens.MarkUsed(ctx, tokenHash, now); err != nil {
			return Caller{}, status.Error(codes.Internal, "record admin token usage")
		}
		return Caller{Kind: CallerAdmin}, nil
	case security.AgentTokenPrefix:
		token, err := a.repos.AgentTokens.FindByHash(ctx, tokenHash)
		if errors.Is(err, db.ErrNotFound) {
			return Caller{}, status.Error(codes.Unauthenticated, "invalid bearer token")
		}
		if err != nil {
			return Caller{}, status.Error(codes.Internal, "authenticate agent token")
		}
		if token.RevokedAt != nil {
			return Caller{}, status.Error(codes.PermissionDenied, "agent token is revoked")
		}
		if isAdminOnlyMethod(fullMethod) {
			return Caller{}, status.Error(codes.PermissionDenied, "agent token cannot call admin RPC")
		}
		if err := a.repos.AgentTokens.MarkUsed(ctx, tokenHash, now); err != nil {
			return Caller{}, status.Error(codes.Internal, "record agent token usage")
		}
		return Caller{Kind: CallerAgent, NodeID: token.NodeID}, nil
	case security.JoinTokenPrefix:
		if fullMethod != "/deployer.v1.NodeService/JoinNode" {
			return Caller{}, status.Error(codes.PermissionDenied, "join token cannot call this RPC")
		}
		token, err := a.repos.JoinTokens.FindByHash(ctx, tokenHash)
		if errors.Is(err, db.ErrNotFound) {
			return Caller{}, status.Error(codes.Unauthenticated, "invalid bearer token")
		}
		if err != nil {
			return Caller{}, status.Error(codes.Internal, "authenticate join token")
		}
		if token.UsedAt != nil || !token.ExpiresAt.After(now) {
			return Caller{}, status.Error(codes.PermissionDenied, "join token is not active")
		}
		return Caller{Kind: CallerJoin}, nil
	default:
		return Caller{}, status.Error(codes.Unauthenticated, "invalid bearer token")
	}
}

func bearerToken(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) == 0 {
		return "", status.Error(codes.Unauthenticated, "authorization bearer token is required")
	}
	value := strings.TrimSpace(values[0])
	scheme, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", status.Error(codes.Unauthenticated, "authorization bearer token is required")
	}
	return strings.TrimSpace(token), nil
}

func isPublicMethod(fullMethod string) bool {
	return fullMethod == "/deployer.v1.PlatformService/GetVersion" ||
		strings.HasPrefix(fullMethod, "/grpc.health.v1.Health/")
}

func isAdminOnlyMethod(fullMethod string) bool {
	return !strings.HasPrefix(fullMethod, "/deployer.v1.NodeService/HeartbeatNode")
}
