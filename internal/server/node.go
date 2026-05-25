package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
	"github.com/0xivanov/self-hosted-deployer/internal/wireguard"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	nodeStatusPending          = "pending"
	nodeStatusOnline           = "online"
	nodeStatusOffline          = "offline"
	defaultJoinTTL             = time.Hour
	defaultNodeOfflineAfter    = 2 * time.Minute
	defaultNodeMonitorInterval = 30 * time.Second
)

type NodeRepository interface {
	Create(ctx context.Context, node domain.Node) error
	FindByID(ctx context.Context, nodeID string) (domain.Node, error)
	FindByName(ctx context.Context, name string) (domain.Node, error)
	List(ctx context.Context) ([]domain.Node, error)
	UpdateStatus(ctx context.Context, nodeID string, status string, updatedAt time.Time) error
	UpdateHeartbeat(ctx context.Context, nodeID string, heartbeat domain.Node, seenAt time.Time) error
	SetWireGuard(ctx context.Context, nodeID string, wireGuardIP string, publicKey string, updatedAt time.Time) error
}

type consumableJoinTokenRepository interface {
	Create(ctx context.Context, token domain.JoinToken) error
	FindByHash(ctx context.Context, tokenHash string) (domain.JoinToken, error)
	Consume(ctx context.Context, tokenHash string, usedAt time.Time) (domain.JoinToken, error)
}

type creatableAgentTokenRepository interface {
	Create(ctx context.Context, token domain.AgentToken) error
}

type NodeServiceConfig struct {
	Nodes        NodeRepository
	JoinTokens   consumableJoinTokenRepository
	AgentTokens  creatableAgentTokenRepository
	TokenHashKey string
	Events       EventRecorder
}

type NodeService struct {
	deployerv1.UnimplementedNodeServiceServer
	nodes       NodeRepository
	joinTokens  consumableJoinTokenRepository
	agentTokens creatableAgentTokenRepository
	hashKey     []byte
	events      EventRecorder
	now         func() time.Time
}

func NewNodeService(cfg NodeServiceConfig) NodeService {
	return NodeService{
		nodes:       cfg.Nodes,
		joinTokens:  cfg.JoinTokens,
		agentTokens: cfg.AgentTokens,
		hashKey:     []byte(cfg.TokenHashKey),
		events:      cfg.Events,
		now:         time.Now,
	}
}

func (s NodeService) CreateJoinToken(ctx context.Context, req *deployerv1.CreateJoinTokenRequest) (*deployerv1.CreateJoinTokenResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}

	nodeName := strings.TrimSpace(req.GetNodeName())
	if nodeName == "" {
		return nil, status.Error(codes.InvalidArgument, "node name is required")
	}

	now := s.now().UTC()
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	if ttl <= 0 {
		ttl = defaultJoinTTL
	}
	expiresAt := now.Add(ttl)

	labelsJSON, err := marshalLabels(req.GetLabels())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	existingNode, err := s.nodes.FindByName(ctx, nodeName)
	if err == nil && existingNode.Status != nodeStatusPending {
		return nil, status.Error(codes.AlreadyExists, "node already exists")
	}
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.Internal, "find node")
	}
	nodeExists := err == nil
	wireGuardIP := existingNode.WireGuardIP
	if wireGuardIP == "" {
		wireGuardIP, err = s.allocateWireGuardIP(ctx)
		if err != nil {
			return nil, err
		}
	}

	rawToken, err := security.NewToken(security.JoinTokenPrefix)
	if err != nil {
		return nil, status.Error(codes.Internal, "create join token")
	}
	tokenHash, err := security.HashToken(s.hashKey, rawToken)
	if err != nil {
		return nil, status.Error(codes.Internal, "hash join token")
	}

	if err := s.joinTokens.Create(ctx, domain.JoinToken{
		TokenHash:        tokenHash,
		IntendedNodeName: nodeName,
		LabelsJSON:       labelsJSON,
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
	}); err != nil {
		return nil, status.Error(codes.Internal, "store join token")
	}

	if !nodeExists {
		nodeID, err := newID("node")
		if err != nil {
			return nil, status.Error(codes.Internal, "create node id")
		}
		node := domain.Node{
			ID:          nodeID,
			Name:        nodeName,
			Status:      nodeStatusPending,
			LabelsJSON:  labelsJSON,
			Arch:        req.GetLabels()["arch"],
			WireGuardIP: wireGuardIP,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.nodes.Create(ctx, node); err != nil {
			return nil, status.Error(codes.Internal, "create pending node")
		}
	} else if existingNode.WireGuardIP == "" {
		if err := s.nodes.SetWireGuard(ctx, existingNode.ID, wireGuardIP, existingNode.WireGuardPublicKey, now); err != nil {
			return nil, status.Error(codes.Internal, "assign WireGuard address")
		}
	}

	return &deployerv1.CreateJoinTokenResponse{
		JoinToken: rawToken,
		ExpiresAt: formatProtoTime(expiresAt),
		NodeName:  nodeName,
	}, nil
}

func (s NodeService) JoinNode(ctx context.Context, req *deployerv1.JoinNodeRequest) (*deployerv1.JoinNodeResponse, error) {
	rawToken := strings.TrimSpace(req.GetJoinToken())
	if rawToken == "" {
		return nil, status.Error(codes.Unauthenticated, "join token is required")
	}
	publicKey := strings.TrimSpace(req.GetPublicKey())
	if err := wireguard.ValidatePublicKey(publicKey); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if _, err := security.Prefix(rawToken); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid join token")
	}
	tokenHash, err := security.HashToken(s.hashKey, rawToken)
	if err != nil {
		return nil, status.Error(codes.Internal, "hash join token")
	}

	now := s.now().UTC()
	joinToken, err := s.joinTokens.FindByHash(ctx, tokenHash)
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.Unauthenticated, "invalid join token")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "find join token")
	}
	if joinToken.UsedAt != nil {
		return nil, status.Error(codes.PermissionDenied, "join token already used")
	}
	if !joinToken.ExpiresAt.After(now) {
		return nil, status.Error(codes.PermissionDenied, "join token expired")
	}

	joinResponse, err := s.enrollNode(ctx, joinToken, req, now)
	if err != nil {
		return nil, err
	}

	if _, err := s.joinTokens.Consume(ctx, tokenHash, now); errors.Is(err, db.ErrJoinTokenExpired) {
		return nil, status.Error(codes.PermissionDenied, "join token expired")
	} else if errors.Is(err, db.ErrJoinTokenUsed) {
		return nil, status.Error(codes.PermissionDenied, "join token already used")
	} else if err != nil {
		return nil, status.Error(codes.Internal, "consume join token")
	}
	recordEvent(ctx, s.events, domain.Event{
		Type:         domain.EventTypeNodeJoined,
		Severity:     domain.EventSeverityInfo,
		Message:      fmt.Sprintf("node %s joined the platform", joinResponse.GetNodeName()),
		NodeID:       joinResponse.GetNodeId(),
		MetadataJSON: metadataJSON(map[string]any{"node_name": joinResponse.GetNodeName()}),
	})
	recordEvent(ctx, s.events, domain.Event{
		Type:         domain.EventTypeNodeOnline,
		Severity:     domain.EventSeverityInfo,
		Message:      fmt.Sprintf("node %s is online", joinResponse.GetNodeName()),
		NodeID:       joinResponse.GetNodeId(),
		MetadataJSON: metadataJSON(map[string]any{"node_name": joinResponse.GetNodeName()}),
	})

	return joinResponse, nil
}

func (s NodeService) allocateWireGuardIP(ctx context.Context) (string, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return "", status.Error(codes.Internal, "list nodes for WireGuard address allocation")
	}
	existingIPs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		existingIPs = append(existingIPs, node.WireGuardIP)
	}
	wireGuardIP, err := wireguard.NextPeerIP(wireguard.DefaultSubnet, wireguard.DefaultHubIP, existingIPs)
	if err != nil {
		return "", status.Error(codes.ResourceExhausted, err.Error())
	}
	return wireGuardIP, nil
}

func (s NodeService) enrollNode(ctx context.Context, joinToken domain.JoinToken, req *deployerv1.JoinNodeRequest, now time.Time) (*deployerv1.JoinNodeResponse, error) {
	nodeName := strings.TrimSpace(joinToken.IntendedNodeName)
	if nodeName == "" {
		nodeName = strings.TrimSpace(req.GetHostname())
	}
	if nodeName == "" {
		return nil, status.Error(codes.InvalidArgument, "node name is required")
	}

	labels := unmarshalLabels(joinToken.LabelsJSON)
	if req.GetArch() != "" {
		labels["arch"] = req.GetArch()
	}
	labelsJSON, err := marshalLabels(labels)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	node, err := s.nodes.FindByName(ctx, nodeName)
	if errors.Is(err, db.ErrNotFound) {
		nodeID, err := newID("node")
		if err != nil {
			return nil, status.Error(codes.Internal, "create node id")
		}
		node = domain.Node{
			ID:        nodeID,
			Name:      nodeName,
			Status:    nodeStatusPending,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.nodes.Create(ctx, node); err != nil {
			return nil, status.Error(codes.Internal, "create node")
		}
	} else if err != nil {
		return nil, status.Error(codes.Internal, "find node")
	}

	wireGuardIP := node.WireGuardIP
	if wireGuardIP == "" {
		wireGuardIP, err = s.allocateWireGuardIP(ctx)
		if err != nil {
			return nil, err
		}
	}
	if err := s.nodes.SetWireGuard(ctx, node.ID, wireGuardIP, strings.TrimSpace(req.GetPublicKey()), now); err != nil {
		return nil, status.Error(codes.Internal, "store WireGuard peer metadata")
	}

	heartbeat := domain.Node{
		Status:     nodeStatusOnline,
		LabelsJSON: labelsJSON,
		Hostname:   strings.TrimSpace(req.GetHostname()),
		Arch:       strings.TrimSpace(req.GetArch()),
	}
	if err := s.nodes.UpdateHeartbeat(ctx, node.ID, heartbeat, now); err != nil {
		return nil, status.Error(codes.Internal, "activate node")
	}

	rawAgentToken, err := security.NewToken(security.AgentTokenPrefix)
	if err != nil {
		return nil, status.Error(codes.Internal, "create agent token")
	}
	agentTokenHash, err := security.HashToken(s.hashKey, rawAgentToken)
	if err != nil {
		return nil, status.Error(codes.Internal, "hash agent token")
	}
	if err := s.agentTokens.Create(ctx, domain.AgentToken{
		TokenHash: agentTokenHash,
		NodeID:    node.ID,
		CreatedAt: now,
	}); err != nil {
		return nil, status.Error(codes.Internal, "store agent token")
	}

	return &deployerv1.JoinNodeResponse{
		NodeId:      node.ID,
		NodeName:    nodeName,
		AgentToken:  rawAgentToken,
		WireguardIp: wireGuardIP,
	}, nil
}

func (s NodeService) ListNodes(ctx context.Context, _ *deployerv1.ListNodesRequest) (*deployerv1.ListNodesResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "list nodes")
	}
	response := &deployerv1.ListNodesResponse{
		Nodes: make([]*deployerv1.Node, 0, len(nodes)),
	}
	for _, node := range nodes {
		response.Nodes = append(response.Nodes, protoNode(node))
	}
	return response, nil
}

func (s NodeService) GetNode(ctx context.Context, req *deployerv1.GetNodeRequest) (*deployerv1.GetNodeResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(req.GetNodeRef())
	if ref == "" {
		return nil, status.Error(codes.InvalidArgument, "node reference is required")
	}
	node, err := s.nodes.FindByName(ctx, ref)
	if errors.Is(err, db.ErrNotFound) {
		node, err = s.nodes.FindByID(ctx, ref)
	}
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "node not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "get node")
	}
	return &deployerv1.GetNodeResponse{Node: protoNode(node)}, nil
}

func (s NodeService) HeartbeatNode(ctx context.Context, req *deployerv1.HeartbeatNodeRequest) (*deployerv1.HeartbeatNodeResponse, error) {
	caller, ok := CallerFromContext(ctx)
	if !ok || caller.Kind != CallerAgent {
		return nil, status.Error(codes.PermissionDenied, "agent token is required")
	}

	statusValue := strings.TrimSpace(req.GetStatus())
	if statusValue == "" {
		statusValue = nodeStatusOnline
	}
	previous, err := s.nodes.FindByID(ctx, caller.NodeID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "node not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "get heartbeat node")
	}
	now := s.now().UTC()
	if err := s.nodes.UpdateHeartbeat(ctx, caller.NodeID, domain.Node{
		Status:   statusValue,
		Hostname: strings.TrimSpace(req.GetHostname()),
		Arch:     strings.TrimSpace(req.GetArch()),
		OS:       strings.TrimSpace(req.GetOs()),
		Kernel:   strings.TrimSpace(req.GetKernel()),
	}, now); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "node not found")
		}
		return nil, status.Error(codes.Internal, "update heartbeat")
	}
	if previous.Status != statusValue {
		switch statusValue {
		case nodeStatusOnline:
			recordEvent(ctx, s.events, domain.Event{
				Type:         domain.EventTypeNodeOnline,
				Severity:     domain.EventSeverityInfo,
				Message:      fmt.Sprintf("node %s is online", previous.Name),
				NodeID:       previous.ID,
				MetadataJSON: metadataJSON(map[string]any{"node_name": previous.Name}),
			})
		case nodeStatusOffline:
			recordEvent(ctx, s.events, domain.Event{
				Type:         domain.EventTypeNodeOffline,
				Severity:     domain.EventSeverityWarning,
				Message:      fmt.Sprintf("node %s is offline", previous.Name),
				NodeID:       previous.ID,
				MetadataJSON: metadataJSON(map[string]any{"node_name": previous.Name}),
			})
		}
	}
	return &deployerv1.HeartbeatNodeResponse{AcceptedAt: formatProtoTime(now)}, nil
}

func MarkOfflineNodes(ctx context.Context, nodes NodeRepository, events EventRecorder, now time.Time, offlineAfter time.Duration) error {
	if nodes == nil {
		return nil
	}
	knownNodes, err := nodes.List(ctx)
	if err != nil {
		return err
	}
	for _, node := range knownNodes {
		if node.Status != nodeStatusOnline || node.LastSeenAt == nil || now.Sub(*node.LastSeenAt) < offlineAfter {
			continue
		}
		if err := nodes.UpdateStatus(ctx, node.ID, nodeStatusOffline, now); err != nil {
			return err
		}
		recordEvent(ctx, events, domain.Event{
			Type:         domain.EventTypeNodeOffline,
			Severity:     domain.EventSeverityWarning,
			Message:      fmt.Sprintf("node %s is offline", node.Name),
			NodeID:       node.ID,
			MetadataJSON: metadataJSON(map[string]any{"node_name": node.Name}),
		})
	}
	return nil
}

func RunNodeOfflineMonitor(ctx context.Context, nodes NodeRepository, events EventRecorder, logger *slog.Logger) {
	check := func() {
		if err := MarkOfflineNodes(ctx, nodes, events, time.Now().UTC(), defaultNodeOfflineAfter); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "mark offline nodes", "error", err)
		}
	}
	check()
	ticker := time.NewTicker(defaultNodeMonitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

func requireCaller(ctx context.Context, kind CallerKind) error {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "authorization bearer token is required")
	}
	if caller.Kind != kind {
		return status.Error(codes.PermissionDenied, fmt.Sprintf("%s token is required", kind))
	}
	return nil
}

func protoNode(node domain.Node) *deployerv1.Node {
	return &deployerv1.Node{
		Id:                 node.ID,
		Name:               node.Name,
		Status:             node.Status,
		Labels:             unmarshalLabels(node.LabelsJSON),
		CreatedAt:          formatProtoTime(node.CreatedAt),
		UpdatedAt:          formatProtoTime(node.UpdatedAt),
		LastSeenAt:         formatOptionalProtoTime(node.LastSeenAt),
		Hostname:           node.Hostname,
		Arch:               node.Arch,
		Os:                 node.OS,
		Kernel:             node.Kernel,
		WireguardIp:        node.WireGuardIP,
		WireguardPublicKey: node.WireGuardPublicKey,
	}
}

func marshalLabels(labels map[string]string) (string, error) {
	if labels == nil {
		return "{}", nil
	}
	clean := make(map[string]string, len(labels))
	for key, value := range labels {
		key = strings.TrimSpace(key)
		if key == "" {
			return "", errors.New("label keys cannot be empty")
		}
		clean[key] = strings.TrimSpace(value)
	}
	data, err := json.Marshal(clean)
	if err != nil {
		return "", fmt.Errorf("encode labels: %w", err)
	}
	return string(data), nil
}

func unmarshalLabels(labelsJSON string) map[string]string {
	if strings.TrimSpace(labelsJSON) == "" {
		return map[string]string{}
	}
	labels := map[string]string{}
	if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
		return map[string]string{}
	}
	return labels
}

func formatProtoTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalProtoTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatProtoTime(*t)
}

func newID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
