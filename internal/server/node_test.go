package server

import (
	"context"
	"errors"
	"strings"
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
	eventRecorder := &recordingEventRecorder{}
	service := NewNodeService(NodeServiceConfig{
		Nodes:        nodes,
		JoinTokens:   joinTokens,
		AgentTokens:  agentTokens,
		TokenHashKey: "hash-key",
		Events:       eventRecorder,
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
	if !eventRecorder.hasType(domain.EventTypeNodeJoined) || !eventRecorder.hasType(domain.EventTypeNodeOnline) {
		t.Fatalf("expected node lifecycle events, got %#v", eventRecorder.events)
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
	if _, err := service.HeartbeatNode(WithCaller(ctx, Caller{Kind: CallerAgent, NodeID: pending.ID}), &deployerv1.HeartbeatNodeRequest{Status: "offline"}); err != nil {
		t.Fatalf("offline heartbeat: %v", err)
	}
	if !eventRecorder.hasType(domain.EventTypeNodeOffline) {
		t.Fatalf("expected offline transition event, got %#v", eventRecorder.events)
	}
	if _, err := service.HeartbeatNode(WithCaller(ctx, Caller{Kind: CallerAgent, NodeID: pending.ID}), &deployerv1.HeartbeatNodeRequest{Status: "online"}); err != nil {
		t.Fatalf("recovery heartbeat: %v", err)
	}
	onlineEvents := 0
	for _, event := range eventRecorder.events {
		if event.Type == domain.EventTypeNodeOnline {
			onlineEvents++
		}
	}
	if onlineEvents != 2 {
		t.Fatalf("expected join and recovery online transitions, got %#v", eventRecorder.events)
	}
}

func TestNodeServiceHeartbeatRequiresAgentCaller(t *testing.T) {
	service := NewNodeService(NodeServiceConfig{})
	_, err := service.HeartbeatNode(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), &deployerv1.HeartbeatNodeRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestMarkOfflineNodesRecordsSingleTransition(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	nodes := db.NewNodeRepository(database)
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	lastSeen := now.Add(-3 * time.Minute)
	if err := nodes.Create(ctx, domain.Node{
		ID:         "node-stale",
		Name:       "pi-stale",
		Status:     nodeStatusOnline,
		LabelsJSON: "{}",
		CreatedAt:  lastSeen,
		UpdatedAt:  lastSeen,
		LastSeenAt: &lastSeen,
	}); err != nil {
		t.Fatalf("create online node: %v", err)
	}
	events := &recordingEventRecorder{}
	if err := MarkOfflineNodes(ctx, nodes, events, now, defaultNodeOfflineAfter); err != nil {
		t.Fatalf("mark offline nodes: %v", err)
	}
	if err := MarkOfflineNodes(ctx, nodes, events, now.Add(time.Minute), defaultNodeOfflineAfter); err != nil {
		t.Fatalf("repeat offline scan: %v", err)
	}
	node, err := nodes.FindByID(ctx, "node-stale")
	if err != nil || node.Status != nodeStatusOffline {
		t.Fatalf("expected offline node, got %#v: %v", node, err)
	}
	if len(events.events) != 1 || events.events[0].Type != domain.EventTypeNodeOffline {
		t.Fatalf("expected one offline transition event, got %#v", events.events)
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

func TestNodeServiceDrainUncordonAndRemoveLifecycle(t *testing.T) {
	ctx := context.Background()
	adminCtx := WithCaller(ctx, Caller{Kind: CallerAdmin})
	database := openTestDB(t)
	nodes := db.NewNodeRepository(database)
	agentTokens := db.NewAgentTokenRepository(database)
	events := &recordingEventRecorder{}
	runtime := &recordingNodeRuntime{readiness: "ready", schedulable: true}
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	if err := nodes.Create(ctx, domain.Node{
		ID:                 "node-live",
		Name:               "pi-kitchen",
		Status:             nodeStatusOnline,
		LabelsJSON:         "{}",
		WireGuardIP:        "10.8.0.2",
		WireGuardPublicKey: validWireGuardPublicKey,
		LastSeenAt:         &now,
		CreatedAt:          now,
		UpdatedAt:          now,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := agentTokens.Create(ctx, domain.AgentToken{TokenHash: "agent-hash", NodeID: "node-live", CreatedAt: now}); err != nil {
		t.Fatalf("create agent token: %v", err)
	}
	peers := &recordingPeerSynchronizer{}
	service := NewNodeService(NodeServiceConfig{
		Nodes:        nodes,
		AgentTokens:  agentTokens,
		Runtime:      runtime,
		Peers:        peers,
		Events:       events,
		OfflineAfter: 2 * time.Minute,
	})
	service.now = func() time.Time { return now.Add(time.Minute) }

	drained, err := service.DrainNode(adminCtx, &deployerv1.DrainNodeRequest{NodeRef: "pi-kitchen"})
	if err != nil || drained.GetNode().GetStatus() != nodeStatusDrained || runtime.cordons != 1 || runtime.drains != 1 {
		t.Fatalf("unexpected drain response=%#v runtime=%#v err=%v", drained, runtime, err)
	}
	if _, err := service.HeartbeatNode(WithCaller(ctx, Caller{Kind: CallerAgent, NodeID: "node-live"}), &deployerv1.HeartbeatNodeRequest{Status: nodeStatusOnline}); err != nil {
		t.Fatalf("heartbeat drained node: %v", err)
	}
	stored, _ := nodes.FindByID(ctx, "node-live")
	if stored.Status != nodeStatusDrained {
		t.Fatalf("heartbeat must retain drained state: %#v", stored)
	}
	uncordoned, err := service.UncordonNode(adminCtx, &deployerv1.UncordonNodeRequest{NodeRef: "pi-kitchen"})
	if err != nil || uncordoned.GetNode().GetStatus() != nodeStatusOnline || runtime.uncordons != 1 {
		t.Fatalf("unexpected uncordon response=%#v runtime=%#v err=%v", uncordoned, runtime, err)
	}
	removed, err := service.RemoveNode(adminCtx, &deployerv1.RemoveNodeRequest{NodeRef: "pi-kitchen"})
	if err != nil || removed.GetNode().GetStatus() != nodeStatusRemoved || runtime.removals != 1 {
		t.Fatalf("unexpected remove response=%#v runtime=%#v err=%v", removed, runtime, err)
	}
	stored, _ = nodes.FindByID(ctx, "node-live")
	if stored.WireGuardIP != "10.8.0.2" || stored.WireGuardPublicKey != "" {
		t.Fatalf("removed node must reserve its WireGuard address without retaining a peer key: %#v", stored)
	}
	nextIP, err := service.allocateWireGuardIP(ctx)
	if err != nil || nextIP != "10.8.0.3" {
		t.Fatalf("removed node WireGuard address must not be reused, got %q: %v", nextIP, err)
	}
	token, err := agentTokens.FindByHash(ctx, "agent-hash")
	if err != nil || token.RevokedAt == nil {
		t.Fatalf("removed node token not revoked: %#v err=%v", token, err)
	}
	if _, err := service.HeartbeatNode(WithCaller(ctx, Caller{Kind: CallerAgent, NodeID: "node-live"}), &deployerv1.HeartbeatNodeRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("removed node heartbeat should fail, got %v", err)
	}
	if _, err := service.RemoveNode(adminCtx, &deployerv1.RemoveNodeRequest{NodeRef: "pi-kitchen"}); err != nil {
		t.Fatalf("repeat removal should be idempotent: %v", err)
	}
	removedEvents := 0
	for _, event := range events.events {
		if event.Type == domain.EventTypeNodeRemoved {
			removedEvents++
		}
	}
	if removedEvents != 1 {
		t.Fatalf("expected one removal event, got %#v", events.events)
	}
	if peers.calls != 2 || len(peers.lastNodes) != 1 || peers.lastNodes[0].Status != nodeStatusRemoved {
		t.Fatalf("expected removal to synchronize disabled hub peer, got %#v", peers)
	}
}

func TestNodeServiceReturnsAuthenticatedWorkerBootstrapMaterial(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	nodes := db.NewNodeRepository(database)
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	if err := nodes.Create(ctx, domain.Node{
		ID: "node-1", Name: "pi-kitchen", Status: nodeStatusOnline, LabelsJSON: "{}",
		WireGuardIP: "10.8.0.2", WireGuardPublicKey: validWireGuardPublicKey,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	peers := &recordingPeerSynchronizer{}
	events := &recordingEventRecorder{}
	service := NewNodeService(NodeServiceConfig{
		Nodes: nodes, Peers: peers, WorkerJoin: fixedWorkerJoinMaterial{},
		Events: events,
		Network: WorkerNetworkConfig{
			HubIP: "10.8.0.1", HubPublicKey: validWireGuardPublicKey, Endpoint: "deploy.example.com:51820",
		},
	})
	response, err := service.GetWorkerBootstrap(
		WithCaller(ctx, Caller{Kind: CallerAgent, NodeID: "node-1"}),
		&deployerv1.GetWorkerBootstrapRequest{},
	)
	if err != nil {
		t.Fatalf("get worker bootstrap: %v", err)
	}
	if response.GetNodeName() != "pi-kitchen" || response.GetWireguardIp() != "10.8.0.2" ||
		response.GetWireguardSubnet() != "10.8.0.0/24" || response.GetK3SUrl() != "https://10.8.0.1:6443" ||
		response.GetK3SToken() != "worker-token" || peers.calls != 1 {
		t.Fatalf("unexpected worker bootstrap response=%#v peers=%#v", response, peers)
	}
	if !events.hasType(domain.EventTypeNodeWorkerBootstrapRequested) ||
		strings.Contains(events.events[0].MetadataJSON, "worker-token") {
		t.Fatalf("expected non-secret worker bootstrap audit event: %#v", events.events)
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

type recordingNodeRuntime struct {
	readiness   string
	schedulable bool
	cordons     int
	drains      int
	uncordons   int
	removals    int
	labelSyncs  int
}

func (r *recordingNodeRuntime) NodeReadiness(context.Context, string) (string, string, bool, error) {
	if r.removals > 0 {
		return "missing", "Kubernetes node was not found", false, nil
	}
	return r.readiness, "", r.schedulable && r.cordons == r.uncordons, nil
}

func (r *recordingNodeRuntime) CordonNode(context.Context, string) error {
	r.cordons++
	return nil
}

func (r *recordingNodeRuntime) DrainNode(ctx context.Context, name string) error {
	r.drains++
	return r.CordonNode(ctx, name)
}

func (r *recordingNodeRuntime) UncordonNode(context.Context, string) error {
	r.uncordons++
	return nil
}

func (r *recordingNodeRuntime) RemoveNode(context.Context, string) error {
	r.removals++
	return nil
}

func (r *recordingNodeRuntime) SyncNodeLabels(context.Context, domain.Node) error {
	r.labelSyncs++
	return nil
}

type recordingPeerSynchronizer struct {
	calls     int
	lastNodes []domain.Node
}

func (s *recordingPeerSynchronizer) SyncPeers(_ context.Context, nodes []domain.Node) error {
	s.calls++
	s.lastNodes = append([]domain.Node(nil), nodes...)
	return nil
}

type fixedWorkerJoinMaterial struct{}

func (fixedWorkerJoinMaterial) WorkerJoinMaterial(context.Context) (string, string, error) {
	return "https://10.8.0.1:6443", "worker-token", nil
}

func (r failingAgentTokenRepository) Create(context.Context, domain.AgentToken) error {
	return r.err
}

func (r failingAgentTokenRepository) RevokeByNodeID(context.Context, string, time.Time) error {
	return r.err
}
