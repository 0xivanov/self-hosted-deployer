package server

import (
	"context"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
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

	joinResponse, err := service.JoinNode(ctx, &deployerv1.JoinNodeRequest{
		JoinToken: rawJoinToken,
		Hostname:  "pi-kitchen.local",
		Arch:      "linux/arm64",
	})
	if err != nil {
		t.Fatalf("join node: %v", err)
	}
	if joinResponse.GetNodeId() != pending.ID || joinResponse.GetNodeName() != "pi-kitchen" || joinResponse.GetAgentToken() == "" {
		t.Fatalf("unexpected join response: %#v", joinResponse)
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

	_, err = service.JoinNode(ctx, &deployerv1.JoinNodeRequest{JoinToken: rawJoinToken})
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
