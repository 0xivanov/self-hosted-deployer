package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type recordingMutationAuditSink struct {
	records []MutationAuditRecord
	err     error
}

func (s *recordingMutationAuditSink) RecordMutation(record MutationAuditRecord) error {
	s.records = append(s.records, record)
	return s.err
}

func TestMutationAuditRecordsSafeTargetAndOutcome(t *testing.T) {
	sink := &recordingMutationAuditSink{err: errors.New("sink unavailable")}
	now := time.Date(2026, 9, 6, 12, 30, 0, 123000000, time.FixedZone("test", 2*60*60))
	interceptor := newMutationAuditInterceptor(sink, func() time.Time { return now }, func() string { return "request-123" })
	request := &deployerv1.DeployAppRequest{DeployerYaml: "name: customer-api\nsecrets:\n  DB_PASSWORD: do-not-log\n"}
	wantErr := status.Error(codes.InvalidArgument, "sensitive request details")
	var gotRequestID string
	_, err := interceptor(WithCaller(context.Background(), Caller{Kind: CallerAdmin, TokenID: "opaque-token", NodeID: "ignored-for-admin"}), request, &grpc.UnaryServerInfo{FullMethod: "/deployer.v1.AppService/DeployApp"}, func(ctx context.Context, _ any) (any, error) {
		gotRequestID, _ = RequestIDFromContext(ctx)
		return nil, wantErr
	})
	if status.Code(err) != codes.InvalidArgument || gotRequestID != "request-123" {
		t.Fatalf("handler outcome changed: %v", err)
	}
	if len(sink.records) != 1 {
		t.Fatalf("expected one audit record, got %d", len(sink.records))
	}
	record := sink.records[0]
	if record.Timestamp != now.UTC() || record.CorrelationID != "request-123" || record.TokenID != "opaque-token" || record.Outcome != codes.InvalidArgument {
		t.Fatalf("unexpected audit record: %#v", record)
	}
	if record.Target["app"] != "customer-api" || strings.Contains(stringMap(record.Target), "do-not-log") || strings.Contains(stringMap(record.Target), "sensitive request details") {
		t.Fatalf("unsafe audit target: %#v", record.Target)
	}
}

func TestMutationAuditSinkFailureDoesNotChangeMutation(t *testing.T) {
	sink := &recordingMutationAuditSink{err: errors.New("offhost sink down")}
	interceptor := newMutationAuditInterceptor(sink, time.Now, func() string { return "request-456" })
	wantErr := status.Error(codes.PermissionDenied, "denied")
	_, err := interceptor(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.DeleteAppRequest{Name: "customer-api"}, &grpc.UnaryServerInfo{FullMethod: "/deployer.v1.AppService/DeleteApp"}, func(context.Context, any) (any, error) {
		return nil, wantErr
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("audit sink changed handler result: %v", err)
	}
}

func TestMutationAuditRunsAfterAuthentication(t *testing.T) {
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	rawToken, tokenHash := createToken(t, security.AdminTokenPrefix, "hash-key")
	if err := repos.AdminTokens.Create(context.Background(), domain.AdminToken{TokenHash: tokenHash, Name: "audit", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	sink := &recordingMutationAuditSink{}
	auth := NewAuthenticator(repos.Auth(), "hash-key")
	audit := newMutationAuditInterceptor(sink, time.Now, func() string { return "request-789" })
	handler := func(ctx context.Context, req any) (any, error) {
		return audit(ctx, req, &grpc.UnaryServerInfo{FullMethod: "/deployer.v1.SecretService/SetSecret"}, func(context.Context, any) (any, error) { return nil, nil })
	}
	if _, err := auth.UnaryInterceptor()(withBearer(context.Background(), "bad-token"), &deployerv1.SetSecretRequest{AppName: "app", Name: "password", Value: "secret"}, &grpc.UnaryServerInfo{FullMethod: "/deployer.v1.SecretService/SetSecret"}, handler); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated request, got %v", err)
	}
	if len(sink.records) != 0 {
		t.Fatal("unauthenticated mutation was audited")
	}
	if _, err := auth.UnaryInterceptor()(withBearer(context.Background(), rawToken), &deployerv1.SetSecretRequest{AppName: "app", Name: "password", Value: "secret"}, &grpc.UnaryServerInfo{FullMethod: "/deployer.v1.SecretService/SetSecret"}, handler); err != nil {
		t.Fatalf("authenticated mutation failed: %v", err)
	}
	if len(sink.records) != 1 || sink.records[0].Target["app"] != "app" || sink.records[0].Target["secret"] != "password" {
		t.Fatalf("unexpected authenticated audit record: %#v", sink.records)
	}
	if strings.Contains(stringMap(sink.records[0].Target), "=secret;") {
		t.Fatal("secret value was included in audit target")
	}
}

func TestAuthenticatorCallerTokenIDIsOpaque(t *testing.T) {
	database := openTestDB(t)
	repos := newTestTokenRepositories(database)
	rawToken, tokenHash := createToken(t, security.AdminTokenPrefix, "hash-key")
	if err := repos.AdminTokens.Create(context.Background(), domain.AdminToken{TokenHash: tokenHash, Name: "opaque", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthenticator(repos.Auth(), "hash-key")
	_, err := auth.UnaryInterceptor()(withBearer(context.Background(), rawToken), nil, &grpc.UnaryServerInfo{FullMethod: "/deployer.v1.SecretService/SetSecret"}, func(ctx context.Context, _ any) (any, error) {
		caller, ok := CallerFromContext(ctx)
		if !ok {
			t.Fatal("caller missing")
		}
		if caller.TokenID == "" || caller.TokenID == rawToken || caller.TokenID == tokenHash {
			t.Fatalf("token identifier is not opaque: %#v", caller)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func stringMap(values map[string]string) string {
	var builder strings.Builder
	for key, value := range values {
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(value)
		builder.WriteString(";")
	}
	return builder.String()
}
