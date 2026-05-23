package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const defaultRequestTimeout = 10 * time.Second

type PlatformClient struct {
	platformClient deployerv1.PlatformServiceClient
	nodeClient     deployerv1.NodeServiceClient
	token          string
	timeout        time.Duration
}

type ServerStatus struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Ready     bool   `json:"ready"`
}

type NodeInfo struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Labels     map[string]string `json:"labels"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
	LastSeenAt string            `json:"last_seen_at,omitempty"`
	Hostname   string            `json:"hostname,omitempty"`
	Arch       string            `json:"arch,omitempty"`
	OS         string            `json:"os,omitempty"`
	Kernel     string            `json:"kernel,omitempty"`
}

type JoinTokenResult struct {
	NodeName  string `json:"node_name"`
	JoinToken string `json:"join_token"`
	ExpiresAt string `json:"expires_at"`
	Command   string `json:"command"`
}

type JoinResult struct {
	NodeID     string `json:"node_id"`
	NodeName   string `json:"node_name"`
	AgentToken string `json:"agent_token,omitempty"`
}

type Heartbeat struct {
	Status          string
	Hostname        string
	Arch            string
	OS              string
	Kernel          string
	ResourceSummary string
}

func NewPlatformClient(serverURL string, token string) (*PlatformClient, *grpc.ClientConn, error) {
	target, err := NormalizeServerTarget(serverURL)
	if err != nil {
		return nil, nil, err
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("create gRPC client for %q: %w", serverURL, err)
	}

	return NewPlatformClientForServices(deployerv1.NewPlatformServiceClient(conn), deployerv1.NewNodeServiceClient(conn), token), conn, nil
}

func NewPlatformClientForService(client deployerv1.PlatformServiceClient, token string) *PlatformClient {
	return NewPlatformClientForServices(client, nil, token)
}

func NewPlatformClientForServices(platformClient deployerv1.PlatformServiceClient, nodeClient deployerv1.NodeServiceClient, token string) *PlatformClient {
	return &PlatformClient{
		platformClient: platformClient,
		nodeClient:     nodeClient,
		token:          token,
		timeout:        defaultRequestTimeout,
	}
}

func (c *PlatformClient) Status(ctx context.Context) (ServerStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if c.token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
	}

	response, err := c.platformClient.GetStatus(ctx, &deployerv1.GetStatusRequest{})
	if err != nil {
		return ServerStatus{}, DecodeRPCError(err)
	}

	return ServerStatus{
		Version:   response.GetVersion(),
		Commit:    response.GetCommit(),
		BuildDate: response.GetBuildDate(),
		Ready:     response.GetReady(),
	}, nil
}

func (c *PlatformClient) CreateJoinToken(ctx context.Context, nodeName string, labels map[string]string) (JoinTokenResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.nodeClient.CreateJoinToken(ctx, &deployerv1.CreateJoinTokenRequest{
		NodeName: nodeName,
		Labels:   labels,
	})
	if err != nil {
		return JoinTokenResult{}, DecodeRPCError(err)
	}
	return JoinTokenResult{
		NodeName:  response.GetNodeName(),
		JoinToken: response.GetJoinToken(),
		ExpiresAt: response.GetExpiresAt(),
	}, nil
}

func (c *PlatformClient) ListNodes(ctx context.Context) ([]NodeInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.nodeClient.ListNodes(ctx, &deployerv1.ListNodesRequest{})
	if err != nil {
		return nil, DecodeRPCError(err)
	}
	nodes := make([]NodeInfo, 0, len(response.GetNodes()))
	for _, node := range response.GetNodes() {
		nodes = append(nodes, nodeInfo(node))
	}
	return nodes, nil
}

func (c *PlatformClient) GetNode(ctx context.Context, ref string) (NodeInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.nodeClient.GetNode(ctx, &deployerv1.GetNodeRequest{NodeRef: ref})
	if err != nil {
		return NodeInfo{}, DecodeRPCError(err)
	}
	return nodeInfo(response.GetNode()), nil
}

func (c *PlatformClient) JoinNode(ctx context.Context, joinToken string, hostname string, arch string) (JoinResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	response, err := c.nodeClient.JoinNode(ctx, &deployerv1.JoinNodeRequest{
		JoinToken: joinToken,
		Hostname:  hostname,
		Arch:      arch,
	})
	if err != nil {
		return JoinResult{}, DecodeRPCError(err)
	}
	return JoinResult{
		NodeID:     response.GetNodeId(),
		NodeName:   response.GetNodeName(),
		AgentToken: response.GetAgentToken(),
	}, nil
}

func (c *PlatformClient) Heartbeat(ctx context.Context, heartbeat Heartbeat) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	_, err := c.nodeClient.HeartbeatNode(ctx, &deployerv1.HeartbeatNodeRequest{
		Status:          heartbeat.Status,
		Hostname:        heartbeat.Hostname,
		Arch:            heartbeat.Arch,
		Os:              heartbeat.OS,
		Kernel:          heartbeat.Kernel,
		ResourceSummary: heartbeat.ResourceSummary,
	})
	return DecodeRPCError(err)
}

func (c *PlatformClient) withBearer(ctx context.Context) context.Context {
	if c.token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
}

func nodeInfo(node *deployerv1.Node) NodeInfo {
	if node == nil {
		return NodeInfo{}
	}
	return NodeInfo{
		ID:         node.GetId(),
		Name:       node.GetName(),
		Status:     node.GetStatus(),
		Labels:     node.GetLabels(),
		CreatedAt:  node.GetCreatedAt(),
		UpdatedAt:  node.GetUpdatedAt(),
		LastSeenAt: node.GetLastSeenAt(),
		Hostname:   node.GetHostname(),
		Arch:       node.GetArch(),
		OS:         node.GetOs(),
		Kernel:     node.GetKernel(),
	}
}

func NormalizeServerTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("server URL is required; pass --server or run deployer login <server-url>")
	}
	if !strings.Contains(raw, "://") {
		if strings.Contains(raw, "/") {
			return "", fmt.Errorf("invalid server URL %q: expected host:port or http(s)://host:port", raw)
		}
		return raw, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid server URL %q: %w", raw, err)
	}

	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("invalid server URL %q: expected http or https", raw)
	}

	if parsed.Host == "" {
		return "", fmt.Errorf("invalid server URL %q: missing host", raw)
	}
	return parsed.Host, nil
}

func NormalizeServerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("server URL is required")
	}
	if !strings.Contains(raw, "://") {
		if strings.Contains(raw, "/") {
			return "", fmt.Errorf("invalid server URL %q: expected host:port or http(s)://host:port", raw)
		}
		return raw, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid server URL %q: %w", raw, err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid server URL %q: missing host", raw)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func DecodeRPCError(err error) error {
	if err == nil {
		return nil
	}

	rpcStatus, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("call control plane: %w", err)
	}

	switch rpcStatus.Code() {
	case codes.Unauthenticated:
		return fmt.Errorf("authentication failed: %s", rpcStatus.Message())
	case codes.PermissionDenied:
		return fmt.Errorf("permission denied: %s", rpcStatus.Message())
	case codes.InvalidArgument:
		return fmt.Errorf("invalid request: %s", rpcStatus.Message())
	case codes.NotFound:
		return fmt.Errorf("not found: %s", rpcStatus.Message())
	case codes.AlreadyExists:
		return fmt.Errorf("already exists: %s", rpcStatus.Message())
	case codes.Unavailable:
		return fmt.Errorf("control plane unavailable: %s", rpcStatus.Message())
	default:
		return fmt.Errorf("control plane returned %s: %s", strings.ToLower(rpcStatus.Code().String()), rpcStatus.Message())
	}
}
