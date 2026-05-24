package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNodeServiceCreateJoinTokenJoinAndHeartbeat(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	nodes := db.NewNodeRepository(database)
	joinTokens := db.NewJoinTokenRepository(database)
	agentTokens := db.NewAgentTokenRepository(database)
	service := NewNodeService(NodeServiceConfig{
		Nodes:        nodes,
		JoinTokens:   joinTokens,
		AgentTokens:  agentTokens,
		TokenHashKey: "hash-key",
	})

	createResponse, err := service.CreateJoinToken(WithCaller(ctx, Caller{Kind: CallerAdmin}), &deployerv1.CreateJoinTokenRequest{
		NodeName: "pi-kitchen",
		Labels: map[string]string{
			"location": "home",
			"role":     "worker",
		},
	})
	if err != nil {
		t.Fatalf("create join token: %v", err)
	}
	if createResponse.GetJoinToken() == "" || createResponse.GetExpiresAt() == "" {
		t.Fatalf("expected raw token and expiry, got %#v", createResponse)
	}

	rawJoinToken := createResponse.GetJoinToken()
	joinTokenHash, err := security.HashToken([]byte("hash-key"), rawJoinToken)
	if err != nil {
		t.Fatalf("hash join token: %v", err)
	}
	if _, err := joinTokens.FindByHash(ctx, joinTokenHash); err != nil {
		t.Fatalf("expected stored token hash: %v", err)
	}
	if _, err := joinTokens.FindByHash(ctx, rawJoinToken); err != db.ErrNotFound {
		t.Fatalf("raw join token should not be stored, got %v", err)
	}

	pending, err := nodes.FindByName(ctx, "pi-kitchen")
	if err != nil {
		t.Fatalf("find pending node: %v", err)
	}
	if pending.Status != "pending" {
		t.Fatalf("expected pending node, got %#v", pending)
	}
	if pending.WireGuardIP != "10.8.0.2" {
		t.Fatalf("expected allocated WireGuard IP, got %#v", pending)
	}

	joinResponse, err := service.JoinNode(ctx, &deployerv1.JoinNodeRequest{
		JoinToken: rawJoinToken,
		Hostname:  "pi-kitchen.local",
		Arch:      "linux/arm64",
		PublicKey: validWireGuardPublicKey,
	})
	if err != nil {
		t.Fatalf("join node: %v", err)
	}
	if joinResponse.GetNodeId() != pending.ID || joinResponse.GetNodeName() != "pi-kitchen" || joinResponse.GetAgentToken() == "" {
		t.Fatalf("unexpected join response: %#v", joinResponse)
	}
	if joinResponse.GetWireguardIp() != "10.8.0.2" {
		t.Fatalf("expected join response WireGuard IP, got %#v", joinResponse)
	}

	agentTokenHash, err := security.HashToken([]byte("hash-key"), joinResponse.GetAgentToken())
	if err != nil {
		t.Fatalf("hash agent token: %v", err)
	}
	if _, err := agentTokens.FindByHash(ctx, agentTokenHash); err != nil {
		t.Fatalf("expected stored agent token hash: %v", err)
	}

	online, err := nodes.FindByID(ctx, pending.ID)
	if err != nil {
		t.Fatalf("find online node: %v", err)
	}
	if online.Status != "online" || online.LastSeenAt == nil || online.Arch != "linux/arm64" {
		t.Fatalf("expected activated node, got %#v", online)
	}
	if online.WireGuardIP != "10.8.0.2" || online.WireGuardPublicKey != validWireGuardPublicKey {
		t.Fatalf("expected stored WireGuard metadata, got %#v", online)
	}

	_, err = service.JoinNode(ctx, &deployerv1.JoinNodeRequest{JoinToken: rawJoinToken, PublicKey: validWireGuardPublicKey})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected reused join token to fail with PermissionDenied, got %v", err)
	}

	heartbeatResponse, err := service.HeartbeatNode(WithCaller(ctx, Caller{Kind: CallerAgent, NodeID: pending.ID}), &deployerv1.HeartbeatNodeRequest{
		Status:   "online",
		Hostname: "pi-kitchen.local",
		Arch:     "linux/arm64",
		Os:       "linux",
		Kernel:   "6.6",
	})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if heartbeatResponse.GetAcceptedAt() == "" {
		t.Fatal("expected heartbeat accepted timestamp")
	}

	heartbeatNode, err := nodes.FindByID(ctx, pending.ID)
	if err != nil {
		t.Fatalf("find heartbeat node: %v", err)
	}
	if heartbeatNode.OS != "linux" || heartbeatNode.Kernel != "6.6" {
		t.Fatalf("heartbeat metadata was not stored: %#v", heartbeatNode)
	}
}

func TestNodeServiceHeartbeatRequiresAgentCaller(t *testing.T) {
	service := NewNodeService(NodeServiceConfig{})
	_, err := service.HeartbeatNode(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.HeartbeatNodeRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestNodeServiceCanReissueJoinTokenForPendingNode(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	service := NewNodeService(NodeServiceConfig{
		Nodes:        db.NewNodeRepository(database),
		JoinTokens:   db.NewJoinTokenRepository(database),
		AgentTokens:  db.NewAgentTokenRepository(database),
		TokenHashKey: "hash-key",
	})

	first, err := service.CreateJoinToken(WithCaller(ctx, Caller{Kind: CallerAdmin}), &deployerv1.CreateJoinTokenRequest{
		NodeName: "pi-kitchen",
	})
	if err != nil {
		t.Fatalf("create first join token: %v", err)
	}

	second, err := service.CreateJoinToken(WithCaller(ctx, Caller{Kind: CallerAdmin}), &deployerv1.CreateJoinTokenRequest{
		NodeName: "pi-kitchen",
	})
	if err != nil {
		t.Fatalf("reissue join token for pending node: %v", err)
	}
	if second.GetJoinToken() == "" || second.GetJoinToken() == first.GetJoinToken() {
		t.Fatalf("expected distinct replacement token, got first=%q second=%q", first.GetJoinToken(), second.GetJoinToken())
	}
}

func TestNodeServiceAllocatesUniqueWireGuardIPs(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	nodes := db.NewNodeRepository(database)
	service := NewNodeService(NodeServiceConfig{
		Nodes:        nodes,
		JoinTokens:   db.NewJoinTokenRepository(database),
		AgentTokens:  db.NewAgentTokenRepository(database),
		TokenHashKey: "hash-key",
	})

	for _, name := range []string{"pi-kitchen", "pi-office"} {
		if _, err := service.CreateJoinToken(WithCaller(ctx, Caller{Kind: CallerAdmin}), &deployerv1.CreateJoinTokenRequest{NodeName: name}); err != nil {
			t.Fatalf("create join token for %s: %v", name, err)
		}
	}

	first, err := nodes.FindByName(ctx, "pi-kitchen")
	if err != nil {
		t.Fatalf("find first node: %v", err)
	}
	second, err := nodes.FindByName(ctx, "pi-office")
	if err != nil {
		t.Fatalf("find second node: %v", err)
	}
	if first.WireGuardIP != "10.8.0.2" || second.WireGuardIP != "10.8.0.3" {
		t.Fatalf("unexpected WireGuard IP allocation: first=%#v second=%#v", first, second)
	}
}

func TestNodeServiceRejectsJoinWithoutValidWireGuardPublicKey(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	service := NewNodeService(NodeServiceConfig{
		Nodes:        db.NewNodeRepository(database),
		JoinTokens:   db.NewJoinTokenRepository(database),
		AgentTokens:  db.NewAgentTokenRepository(database),
		TokenHashKey: "hash-key",
	})

	createResponse, err := service.CreateJoinToken(WithCaller(ctx, Caller{Kind: CallerAdmin}), &deployerv1.CreateJoinTokenRequest{
		NodeName: "pi-kitchen",
	})
	if err != nil {
		t.Fatalf("create join token: %v", err)
	}

	_, err = service.JoinNode(ctx, &deployerv1.JoinNodeRequest{
		JoinToken: createResponse.GetJoinToken(),
		PublicKey: "not-base64",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestNodeServiceDoesNotCreatePendingNodeWhenJoinTokenStoreFails(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	nodes := db.NewNodeRepository(database)
	service := NewNodeService(NodeServiceConfig{
		Nodes:        nodes,
		JoinTokens:   failingJoinTokenRepository{err: errors.New("write failed")},
		AgentTokens:  db.NewAgentTokenRepository(database),
		TokenHashKey: "hash-key",
	})

	_, err := service.CreateJoinToken(WithCaller(ctx, Caller{Kind: CallerAdmin}), &deployerv1.CreateJoinTokenRequest{
		NodeName: "pi-kitchen",
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
	if _, err := nodes.FindByName(ctx, "pi-kitchen"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("pending node should not be created when token storage fails, got %v", err)
	}
}

func TestNodeServiceDoesNotConsumeJoinTokenWhenAgentTokenStoreFails(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	nodes := db.NewNodeRepository(database)
	joinTokens := db.NewJoinTokenRepository(database)
	service := NewNodeService(NodeServiceConfig{
		Nodes:        nodes,
		JoinTokens:   joinTokens,
		AgentTokens:  failingAgentTokenRepository{err: errors.New("write failed")},
		TokenHashKey: "hash-key",
	})

	createResponse, err := service.CreateJoinToken(WithCaller(ctx, Caller{Kind: CallerAdmin}), &deployerv1.CreateJoinTokenRequest{
		NodeName: "pi-kitchen",
	})
	if err != nil {
		t.Fatalf("create join token: %v", err)
	}

	_, err = service.JoinNode(ctx, &deployerv1.JoinNodeRequest{
		JoinToken: createResponse.GetJoinToken(),
		Hostname:  "pi-kitchen.local",
		Arch:      "linux/arm64",
		PublicKey: validWireGuardPublicKey,
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}

	tokenHash, err := security.HashToken([]byte("hash-key"), createResponse.GetJoinToken())
	if err != nil {
		t.Fatalf("hash join token: %v", err)
	}
	stored, err := joinTokens.FindByHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("find join token: %v", err)
	}
	if stored.UsedAt != nil {
		t.Fatalf("join token should remain unused after failed enrollment: %#v", stored)
	}
}

const validWireGuardPublicKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="

type failingJoinTokenRepository struct {
	err error
}

func (r failingJoinTokenRepository) Create(context.Context, domain.JoinToken) error {
	return r.err
}

func (r failingJoinTokenRepository) FindByHash(context.Context, string) (domain.JoinToken, error) {
	return domain.JoinToken{}, r.err
}

func (r failingJoinTokenRepository) Consume(context.Context, string, time.Time) (domain.JoinToken, error) {
	return domain.JoinToken{}, r.err
}

type failingAgentTokenRepository struct {
	err error
}

func (r failingAgentTokenRepository) Create(context.Context, domain.AgentToken) error {
	return r.err
}
