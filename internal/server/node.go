package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
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
	nodeStatusPending       = "pending"
	nodeStatusOnline        = "online"
	nodeStatusOffline       = "offline"
	nodeStatusDrained       = "drained"
	nodeStatusRemoved       = "removed"
	defaultJoinTTL          = time.Hour
	defaultNodeOfflineAfter = 2 * time.Minute
)

var nodeNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type NodeRepository interface {
	Create(ctx context.Context, node domain.Node) error
	FindByID(ctx context.Context, nodeID string) (domain.Node, error)
	FindByName(ctx context.Context, name string) (domain.Node, error)
	List(ctx context.Context) ([]domain.Node, error)
	UpdateStatus(ctx context.Context, nodeID string, status string, updatedAt time.Time) error
	UpdateHeartbeat(ctx context.Context, nodeID string, heartbeat domain.Node, seenAt time.Time) error
	SetWireGuard(ctx context.Context, nodeID string, wireGuardIP string, publicKey string, updatedAt time.Time) error
	Rename(ctx context.Context, nodeID string, name string, updatedAt time.Time) error
	Delete(ctx context.Context, nodeID string) error
}

type consumableJoinTokenRepository interface {
	Create(ctx context.Context, token domain.JoinToken) error
	FindByHash(ctx context.Context, tokenHash string) (domain.JoinToken, error)
	Consume(ctx context.Context, tokenHash string, usedAt time.Time) (domain.JoinToken, error)
	DeleteByNodeName(ctx context.Context, nodeName string) error
}

type nodeAgentTokenRepository interface {
	Create(ctx context.Context, token domain.AgentToken) error
	RevokeByNodeID(ctx context.Context, nodeID string, revokedAt time.Time) error
	DeleteByNodeID(ctx context.Context, nodeID string) error
}

type NodeRuntime interface {
	NodeReadiness(ctx context.Context, nodeName string) (state string, message string, schedulable bool, err error)
	CordonNode(ctx context.Context, nodeName string) error
	UncordonNode(ctx context.Context, nodeName string) error
	RemoveNode(ctx context.Context, nodeName string) error
}

type NodeDrainRuntime interface {
	DrainNode(ctx context.Context, nodeName string) error
}

type NodeLabelSyncRuntime interface {
	SyncNodeLabels(ctx context.Context, node domain.Node) error
}

type PeerSynchronizer interface {
	SyncPeers(ctx context.Context, nodes []domain.Node) error
}

type WorkerJoinMaterialProvider interface {
	WorkerJoinMaterial(ctx context.Context) (serverURL string, token string, err error)
}

type WorkerNetworkConfig struct {
	Subnet       string
	HubIP        string
	HubPublicKey string
	Endpoint     string
}

type NodeServiceConfig struct {
	Nodes        NodeRepository
	JoinTokens   consumableJoinTokenRepository
	AgentTokens  nodeAgentTokenRepository
	TokenHashKey string
	Events       EventRecorder
	Runtime      NodeRuntime
	Peers        PeerSynchronizer
	WorkerJoin   WorkerJoinMaterialProvider
	Network      WorkerNetworkConfig
	OfflineAfter time.Duration
}

type NodeService struct {
	deployerv1.UnimplementedNodeServiceServer
	nodes        NodeRepository
	joinTokens   consumableJoinTokenRepository
	agentTokens  nodeAgentTokenRepository
	hashKey      []byte
	events       EventRecorder
	runtime      NodeRuntime
	peers        PeerSynchronizer
	workerJoin   WorkerJoinMaterialProvider
	network      WorkerNetworkConfig
	offlineAfter time.Duration
	now          func() time.Time
}

func NewNodeService(cfg NodeServiceConfig) NodeService {
	offlineAfter := cfg.OfflineAfter
	if offlineAfter <= 0 {
		offlineAfter = defaultNodeOfflineAfter
	}
	return NodeService{
		nodes:        cfg.Nodes,
		joinTokens:   cfg.JoinTokens,
		agentTokens:  cfg.AgentTokens,
		hashKey:      []byte(cfg.TokenHashKey),
		events:       cfg.Events,
		runtime:      cfg.Runtime,
		peers:        cfg.Peers,
		workerJoin:   cfg.WorkerJoin,
		network:      cfg.Network,
		offlineAfter: offlineAfter,
		now:          time.Now,
	}
}

func (s NodeService) CreateJoinToken(ctx context.Context, req *deployerv1.CreateJoinTokenRequest) (*deployerv1.CreateJoinTokenResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}

	nodeName := strings.TrimSpace(req.GetNodeName())
	if err := validateNodeName(nodeName); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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
	if node.Status == nodeStatusRemoved {
		return nil, status.Error(codes.PermissionDenied, "node has been removed")
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
		nodeProto, err := s.nodeWithReadiness(ctx, node)
		if err != nil {
			return nil, err
		}
		response.Nodes = append(response.Nodes, nodeProto)
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
	nodeProto, err := s.nodeWithReadiness(ctx, node)
	if err != nil {
		return nil, err
	}
	return &deployerv1.GetNodeResponse{Node: nodeProto}, nil
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
	if previous.Status == nodeStatusRemoved {
		return nil, status.Error(codes.PermissionDenied, "node has been removed")
	}
	if statusValue != nodeStatusOnline && statusValue != nodeStatusOffline {
		return nil, status.Error(codes.InvalidArgument, "status must be online or offline")
	}
	vpnStatus := strings.TrimSpace(req.GetVpnStatus())
	if vpnStatus != "" && vpnStatus != "connected" && vpnStatus != "disconnected" && vpnStatus != "unknown" {
		return nil, status.Error(codes.InvalidArgument, "vpn_status must be connected, disconnected, or unknown")
	}
	effectiveStatus := statusValue
	if previous.Status == nodeStatusDrained {
		effectiveStatus = nodeStatusDrained
	}
	now := s.now().UTC()
	if err := s.nodes.UpdateHeartbeat(ctx, caller.NodeID, domain.Node{
		Status:    effectiveStatus,
		Hostname:  strings.TrimSpace(req.GetHostname()),
		Arch:      strings.TrimSpace(req.GetArch()),
		OS:        strings.TrimSpace(req.GetOs()),
		Kernel:    strings.TrimSpace(req.GetKernel()),
		VPNStatus: vpnStatus,
	}, now); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "node not found")
		}
		return nil, status.Error(codes.Internal, "update heartbeat")
	}
	current := previous
	current.Status = effectiveStatus
	current.Hostname = strings.TrimSpace(req.GetHostname())
	current.Arch = strings.TrimSpace(req.GetArch())
	current.OS = strings.TrimSpace(req.GetOs())
	current.Kernel = strings.TrimSpace(req.GetKernel())
	current.VPNStatus = vpnStatus
	if labelRuntime, ok := s.runtime.(NodeLabelSyncRuntime); ok {
		if err := labelRuntime.SyncNodeLabels(ctx, current); err != nil {
			return nil, status.Error(codes.Internal, "sync Kubernetes node labels")
		}
	}
	if previous.Status != effectiveStatus {
		switch effectiveStatus {
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

func (s NodeService) GetWorkerBootstrap(ctx context.Context, _ *deployerv1.GetWorkerBootstrapRequest) (*deployerv1.GetWorkerBootstrapResponse, error) {
	caller, ok := CallerFromContext(ctx)
	if !ok || caller.Kind != CallerAgent {
		return nil, status.Error(codes.PermissionDenied, "agent token is required")
	}
	node, err := s.nodes.FindByID(ctx, caller.NodeID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "node not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "get bootstrap node")
	}
	if node.Status == nodeStatusRemoved {
		return nil, status.Error(codes.PermissionDenied, "node has been removed")
	}
	if strings.TrimSpace(node.WireGuardIP) == "" || s.peers == nil {
		return nil, status.Error(codes.FailedPrecondition, "WireGuard peer synchronization is not configured")
	}
	knownNodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "list WireGuard peers")
	}
	if err := s.peers.SyncPeers(ctx, knownNodes); err != nil {
		return nil, status.Error(codes.Internal, "synchronize WireGuard hub peers")
	}
	if s.workerJoin == nil || strings.TrimSpace(s.network.HubIP) == "" ||
		strings.TrimSpace(s.network.HubPublicKey) == "" || strings.TrimSpace(s.network.Endpoint) == "" {
		return nil, status.Error(codes.FailedPrecondition, "worker bootstrap configuration is incomplete")
	}
	if err := wireguard.ValidatePublicKey(s.network.HubPublicKey); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	k3sURL, k3sToken, err := s.workerJoin.WorkerJoinMaterial(ctx)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "k3s worker join material is unavailable")
	}
	subnet := strings.TrimSpace(s.network.Subnet)
	if subnet == "" {
		subnet = wireguard.DefaultSubnet
	}
	recordEvent(ctx, s.events, domain.Event{
		Type:         domain.EventTypeNodeWorkerBootstrapRequested,
		Severity:     domain.EventSeverityInfo,
		Message:      fmt.Sprintf("node %s requested worker bootstrap material", node.Name),
		NodeID:       node.ID,
		MetadataJSON: metadataJSON(map[string]any{"node_name": node.Name}),
	})
	return &deployerv1.GetWorkerBootstrapResponse{
		NodeName:              node.Name,
		WireguardIp:           node.WireGuardIP,
		WireguardSubnet:       subnet,
		WireguardHubIp:        strings.TrimSpace(s.network.HubIP),
		WireguardHubPublicKey: strings.TrimSpace(s.network.HubPublicKey),
		WireguardEndpoint:     strings.TrimSpace(s.network.Endpoint),
		K3SUrl:                k3sURL,
		K3SToken:              k3sToken,
	}, nil
}

func (s NodeService) DrainNode(ctx context.Context, req *deployerv1.DrainNodeRequest) (*deployerv1.DrainNodeResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	node, err := s.findNode(ctx, req.GetNodeRef())
	if err != nil {
		return nil, err
	}
	if node.Status == nodeStatusRemoved {
		return nil, status.Error(codes.FailedPrecondition, "removed node cannot be drained")
	}
	if s.runtime != nil {
		var err error
		if runtime, ok := s.runtime.(NodeDrainRuntime); ok {
			err = runtime.DrainNode(ctx, node.Name)
		} else {
			err = s.runtime.CordonNode(ctx, node.Name)
		}
		if err != nil {
			return nil, status.Error(codes.Internal, "drain Kubernetes node")
		}
	}
	if node.Status != nodeStatusDrained {
		node.Status = nodeStatusDrained
		node.UpdatedAt = s.now().UTC()
		if err := s.nodes.UpdateStatus(ctx, node.ID, node.Status, node.UpdatedAt); err != nil {
			return nil, status.Error(codes.Internal, "mark node drained")
		}
	}
	nodeProto, err := s.nodeWithReadiness(ctx, node)
	if err != nil {
		return nil, err
	}
	return &deployerv1.DrainNodeResponse{Node: nodeProto}, nil
}

func (s NodeService) UncordonNode(ctx context.Context, req *deployerv1.UncordonNodeRequest) (*deployerv1.UncordonNodeResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	node, err := s.findNode(ctx, req.GetNodeRef())
	if err != nil {
		return nil, err
	}
	if node.Status == nodeStatusRemoved {
		return nil, status.Error(codes.FailedPrecondition, "removed node cannot be uncordoned")
	}
	if s.runtime != nil {
		if err := s.runtime.UncordonNode(ctx, node.Name); err != nil {
			return nil, status.Error(codes.Internal, "uncordon Kubernetes node")
		}
	}
	now := s.now().UTC()
	nextStatus := nodeStatusOffline
	if node.LastSeenAt != nil && now.Sub(*node.LastSeenAt) < s.offlineAfter {
		nextStatus = nodeStatusOnline
	}
	if node.Status != nextStatus {
		node.Status = nextStatus
		node.UpdatedAt = now
		if err := s.nodes.UpdateStatus(ctx, node.ID, node.Status, now); err != nil {
			return nil, status.Error(codes.Internal, "mark node schedulable")
		}
	}
	nodeProto, err := s.nodeWithReadiness(ctx, node)
	if err != nil {
		return nil, err
	}
	return &deployerv1.UncordonNodeResponse{Node: nodeProto}, nil
}

func (s NodeService) RemoveNode(ctx context.Context, req *deployerv1.RemoveNodeRequest) (*deployerv1.RemoveNodeResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	node, err := s.findNode(ctx, req.GetNodeRef())
	if err != nil {
		return nil, err
	}
	if node.Status != nodeStatusRemoved {
		now := s.now().UTC()
		if err := s.agentTokens.RevokeByNodeID(ctx, node.ID, now); err != nil {
			return nil, status.Error(codes.Internal, "revoke node identity")
		}
		if err := s.nodes.SetWireGuard(ctx, node.ID, node.WireGuardIP, "", now); err != nil {
			return nil, status.Error(codes.Internal, "disable WireGuard peer")
		}
		if err := s.nodes.UpdateStatus(ctx, node.ID, nodeStatusRemoved, now); err != nil {
			return nil, status.Error(codes.Internal, "mark node removed")
		}
		node.Status = nodeStatusRemoved
		node.WireGuardPublicKey = ""
		node.UpdatedAt = now
		recordEvent(ctx, s.events, domain.Event{
			Type:         domain.EventTypeNodeRemoved,
			Severity:     domain.EventSeverityInfo,
			Message:      fmt.Sprintf("node %s was removed", node.Name),
			NodeID:       node.ID,
			MetadataJSON: metadataJSON(map[string]any{"node_name": node.Name}),
		})
	}
	if s.runtime != nil {
		if err := s.runtime.RemoveNode(ctx, node.Name); err != nil {
			return nil, status.Error(codes.Internal, "remove Kubernetes node")
		}
	}
	if s.peers != nil {
		nodes, err := s.nodes.List(ctx)
		if err != nil {
			return nil, status.Error(codes.Internal, "list WireGuard peers")
		}
		if err := s.peers.SyncPeers(ctx, nodes); err != nil {
			return nil, status.Error(codes.Internal, "synchronize WireGuard hub peers")
		}
	}
	nodeProto, err := s.nodeWithReadiness(ctx, node)
	if err != nil {
		return nil, err
	}
	return &deployerv1.RemoveNodeResponse{Node: nodeProto}, nil
}

func (s NodeService) PurgeNode(ctx context.Context, req *deployerv1.PurgeNodeRequest) (*deployerv1.PurgeNodeResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	node, err := s.findNode(ctx, req.GetNodeRef())
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if s.agentTokens != nil {
		if err := s.agentTokens.RevokeByNodeID(ctx, node.ID, now); err != nil {
			return nil, status.Error(codes.Internal, "revoke node identity")
		}
	}
	if s.runtime != nil {
		if err := s.runtime.RemoveNode(ctx, node.Name); err != nil {
			return nil, status.Error(codes.Internal, "remove Kubernetes node")
		}
	}
	if s.agentTokens != nil {
		if err := s.agentTokens.DeleteByNodeID(ctx, node.ID); err != nil {
			return nil, status.Error(codes.Internal, "delete node agent tokens")
		}
	}
	if s.joinTokens != nil {
		if err := s.joinTokens.DeleteByNodeName(ctx, node.Name); err != nil {
			return nil, status.Error(codes.Internal, "delete node join tokens")
		}
	}
	if err := s.nodes.Delete(ctx, node.ID); err != nil {
		return nil, status.Error(codes.Internal, "delete node")
	}
	if s.peers != nil {
		nodes, err := s.nodes.List(ctx)
		if err != nil {
			return nil, status.Error(codes.Internal, "list WireGuard peers")
		}
		if err := s.peers.SyncPeers(ctx, nodes); err != nil {
			return nil, status.Error(codes.Internal, "synchronize WireGuard hub peers")
		}
	}
	recordEvent(ctx, s.events, domain.Event{
		Type:         domain.EventTypeNodePurged,
		Severity:     domain.EventSeverityWarning,
		Message:      fmt.Sprintf("node %s was purged", node.Name),
		NodeID:       node.ID,
		MetadataJSON: metadataJSON(map[string]any{"node_name": node.Name}),
	})
	return &deployerv1.PurgeNodeResponse{NodeRef: req.GetNodeRef(), NodeName: node.Name}, nil
}

func (s NodeService) RenameNode(ctx context.Context, req *deployerv1.RenameNodeRequest) (*deployerv1.RenameNodeResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	node, err := s.findNode(ctx, req.GetNodeRef())
	if err != nil {
		return nil, err
	}
	newName := strings.TrimSpace(req.GetNewName())
	if err := validateNodeName(newName); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if node.Name == newName {
		nodeProto, err := s.nodeWithReadiness(ctx, node)
		if err != nil {
			return nil, err
		}
		return &deployerv1.RenameNodeResponse{Node: nodeProto}, nil
	}
	switch node.Status {
	case nodeStatusPending, nodeStatusRemoved:
	default:
		return nil, status.Error(codes.FailedPrecondition, "only pending or removed nodes can be renamed; active Kubernetes nodes must be rejoined")
	}
	existing, err := s.nodes.FindByName(ctx, newName)
	if err == nil && existing.ID != node.ID {
		return nil, status.Error(codes.AlreadyExists, "node already exists")
	}
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.Internal, "find node")
	}
	oldName := node.Name
	now := s.now().UTC()
	if err := s.nodes.Rename(ctx, node.ID, newName, now); err != nil {
		return nil, status.Error(codes.Internal, "rename node")
	}
	if s.joinTokens != nil {
		if err := s.joinTokens.DeleteByNodeName(ctx, oldName); err != nil {
			return nil, status.Error(codes.Internal, "delete old node join tokens")
		}
	}
	node.Name = newName
	node.UpdatedAt = now
	recordEvent(ctx, s.events, domain.Event{
		Type:         domain.EventTypeNodeRenamed,
		Severity:     domain.EventSeverityInfo,
		Message:      fmt.Sprintf("node %s was renamed to %s", oldName, newName),
		NodeID:       node.ID,
		MetadataJSON: metadataJSON(map[string]any{"old_name": oldName, "new_name": newName}),
	})
	nodeProto, err := s.nodeWithReadiness(ctx, node)
	if err != nil {
		return nil, err
	}
	return &deployerv1.RenameNodeResponse{Node: nodeProto}, nil
}

func (s NodeService) findNode(ctx context.Context, ref string) (domain.Node, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return domain.Node{}, status.Error(codes.InvalidArgument, "node reference is required")
	}
	node, err := s.nodes.FindByName(ctx, ref)
	if errors.Is(err, db.ErrNotFound) {
		node, err = s.nodes.FindByID(ctx, ref)
	}
	if errors.Is(err, db.ErrNotFound) {
		return domain.Node{}, status.Error(codes.NotFound, "node not found")
	}
	if err != nil {
		return domain.Node{}, status.Error(codes.Internal, "get node")
	}
	return node, nil
}

func validateNodeName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("node name is required")
	}
	if len(name) > 63 || !nodeNamePattern.MatchString(name) {
		return fmt.Errorf("node name must be a DNS-safe Kubernetes name")
	}
	return nil
}

func (s NodeService) nodeWithReadiness(ctx context.Context, node domain.Node) (*deployerv1.Node, error) {
	nodeProto := protoNode(node)
	if s.runtime == nil {
		return nodeProto, nil
	}
	state, message, schedulable, err := s.runtime.NodeReadiness(ctx, node.Name)
	if err != nil {
		return nil, status.Error(codes.Internal, "read Kubernetes node readiness")
	}
	nodeProto.KubernetesStatus = state
	nodeProto.KubernetesMessage = message
	nodeProto.Schedulable = schedulable && node.Status != nodeStatusDrained && node.Status != nodeStatusRemoved
	return nodeProto, nil
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

func RunNodeOfflineMonitor(ctx context.Context, nodes NodeRepository, events EventRecorder, logger *slog.Logger, offlineAfter time.Duration, interval time.Duration) {
	check := func() {
		if err := MarkOfflineNodes(ctx, nodes, events, time.Now().UTC(), offlineAfter); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "mark offline nodes", "error", err)
		}
	}
	check()
	ticker := time.NewTicker(interval)
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
		Schedulable:        node.Status != nodeStatusDrained && node.Status != nodeStatusRemoved,
		VpnStatus:          node.VPNStatus,
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
