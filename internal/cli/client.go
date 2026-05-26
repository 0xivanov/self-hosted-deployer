package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const defaultRequestTimeout = 10 * time.Second

type PlatformClient struct {
	platformClient deployerv1.PlatformServiceClient
	nodeClient     deployerv1.NodeServiceClient
	appClient      deployerv1.AppServiceClient
	secretClient   deployerv1.SecretServiceClient
	eventClient    deployerv1.EventServiceClient
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
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Status             string            `json:"status"`
	Labels             map[string]string `json:"labels"`
	CreatedAt          string            `json:"created_at"`
	UpdatedAt          string            `json:"updated_at"`
	LastSeenAt         string            `json:"last_seen_at,omitempty"`
	Hostname           string            `json:"hostname,omitempty"`
	Arch               string            `json:"arch,omitempty"`
	OS                 string            `json:"os,omitempty"`
	Kernel             string            `json:"kernel,omitempty"`
	WireGuardIP        string            `json:"wireguard_ip,omitempty"`
	WireGuardPublicKey string            `json:"wireguard_public_key,omitempty"`
	KubernetesStatus   string            `json:"kubernetes_status,omitempty"`
	KubernetesMessage  string            `json:"kubernetes_message,omitempty"`
	Schedulable        bool              `json:"schedulable"`
}

type JoinTokenResult struct {
	NodeName  string `json:"node_name"`
	JoinToken string `json:"join_token"`
	ExpiresAt string `json:"expires_at"`
	Command   string `json:"command"`
}

type JoinResult struct {
	NodeID      string `json:"node_id"`
	NodeName    string `json:"node_name"`
	WireGuardIP string `json:"wireguard_ip"`
	AgentToken  string `json:"agent_token,omitempty"`
}

type Heartbeat struct {
	Status          string
	Hostname        string
	Arch            string
	OS              string
	Kernel          string
	ResourceSummary string
}

type AppInfo struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Image        string           `json:"image"`
	Replicas     int              `json:"replicas"`
	Domain       string           `json:"domain"`
	StateMode    string           `json:"state_mode"`
	DesiredState appconfig.Config `json:"desired_state"`
	CreatedAt    string           `json:"created_at"`
	UpdatedAt    string           `json:"updated_at"`
}

type DeploymentInfo struct {
	ID            string `json:"id"`
	AppID         string `json:"app_id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type RouteInfo struct {
	ID         string `json:"id"`
	AppID      string `json:"app_id"`
	Domain     string `json:"domain"`
	TargetPort int    `json:"target_port"`
	Status     string `json:"status"`
	TLSEnabled bool   `json:"tls_enabled"`
}

type EventInfo struct {
	ID           string          `json:"id"`
	CreatedAt    string          `json:"created_at"`
	Type         string          `json:"type"`
	Severity     string          `json:"severity"`
	Message      string          `json:"message"`
	AppID        string          `json:"app_id,omitempty"`
	NodeID       string          `json:"node_id,omitempty"`
	DeploymentID string          `json:"deployment_id,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

type EventFilter struct {
	App      string
	Node     string
	Type     string
	Severity string
	Since    time.Time
	Limit    int
}

type DeployResult struct {
	App        AppInfo        `json:"app"`
	Deployment DeploymentInfo `json:"deployment"`
}

type AppInspectResult struct {
	App         AppInfo          `json:"app"`
	Deployments []DeploymentInfo `json:"deployments"`
	Routes      []RouteInfo      `json:"routes"`
}

type AppStatusResult struct {
	App               AppInfo        `json:"app"`
	LatestDeployment  DeploymentInfo `json:"latest_deployment"`
	Routes            []RouteInfo    `json:"routes"`
	RuntimeStatus     string         `json:"runtime_status"`
	DesiredReplicas   int            `json:"desired_replicas"`
	AvailableReplicas int            `json:"available_replicas"`
	RunningNodes      []string       `json:"running_nodes"`
	Warnings          []string       `json:"warnings"`
}

func NewPlatformClient(serverURL string, token string) (*PlatformClient, *grpc.ClientConn, error) {
	target, transportCredentials, err := dialTargetAndCredentials(serverURL)
	if err != nil {
		return nil, nil, err
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(transportCredentials))
	if err != nil {
		return nil, nil, fmt.Errorf("create gRPC client for %q: %w", serverURL, err)
	}

	client := NewPlatformClientForServices(
		deployerv1.NewPlatformServiceClient(conn),
		deployerv1.NewNodeServiceClient(conn),
		deployerv1.NewAppServiceClient(conn),
		token,
	)
	client.secretClient = deployerv1.NewSecretServiceClient(conn)
	client.eventClient = deployerv1.NewEventServiceClient(conn)
	return client, conn, nil
}

func NewPlatformClientForService(client deployerv1.PlatformServiceClient, token string) *PlatformClient {
	return NewPlatformClientForServices(client, nil, nil, token)
}

func NewPlatformClientForServices(platformClient deployerv1.PlatformServiceClient, nodeClient deployerv1.NodeServiceClient, appClient deployerv1.AppServiceClient, token string) *PlatformClient {
	return &PlatformClient{
		platformClient: platformClient,
		nodeClient:     nodeClient,
		appClient:      appClient,
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

func (c *PlatformClient) DrainNode(ctx context.Context, ref string) (NodeInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)
	response, err := c.nodeClient.DrainNode(ctx, &deployerv1.DrainNodeRequest{NodeRef: ref})
	if err != nil {
		return NodeInfo{}, DecodeRPCError(err)
	}
	return nodeInfo(response.GetNode()), nil
}

func (c *PlatformClient) UncordonNode(ctx context.Context, ref string) (NodeInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)
	response, err := c.nodeClient.UncordonNode(ctx, &deployerv1.UncordonNodeRequest{NodeRef: ref})
	if err != nil {
		return NodeInfo{}, DecodeRPCError(err)
	}
	return nodeInfo(response.GetNode()), nil
}

func (c *PlatformClient) RemoveNode(ctx context.Context, ref string) (NodeInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)
	response, err := c.nodeClient.RemoveNode(ctx, &deployerv1.RemoveNodeRequest{NodeRef: ref})
	if err != nil {
		return NodeInfo{}, DecodeRPCError(err)
	}
	return nodeInfo(response.GetNode()), nil
}

func (c *PlatformClient) JoinNode(ctx context.Context, joinToken string, hostname string, arch string, publicKey string) (JoinResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	response, err := c.nodeClient.JoinNode(ctx, &deployerv1.JoinNodeRequest{
		JoinToken: joinToken,
		Hostname:  hostname,
		Arch:      arch,
		PublicKey: publicKey,
	})
	if err != nil {
		return JoinResult{}, DecodeRPCError(err)
	}
	return JoinResult{
		NodeID:      response.GetNodeId(),
		NodeName:    response.GetNodeName(),
		WireGuardIP: response.GetWireguardIp(),
		AgentToken:  response.GetAgentToken(),
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

func (c *PlatformClient) DeployApp(ctx context.Context, deployerYAML string) (DeployResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.appClient.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: deployerYAML})
	if err != nil {
		return DeployResult{}, DecodeRPCError(err)
	}
	app, err := appInfo(response.GetApp())
	if err != nil {
		return DeployResult{}, err
	}
	return DeployResult{
		App:        app,
		Deployment: deploymentInfo(response.GetDeployment()),
	}, nil
}

func (c *PlatformClient) ListApps(ctx context.Context) ([]AppInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.appClient.ListApps(ctx, &deployerv1.ListAppsRequest{})
	if err != nil {
		return nil, DecodeRPCError(err)
	}
	apps := make([]AppInfo, 0, len(response.GetApps()))
	for _, app := range response.GetApps() {
		info, err := appInfo(app)
		if err != nil {
			return nil, err
		}
		apps = append(apps, info)
	}
	return apps, nil
}

func (c *PlatformClient) InspectApp(ctx context.Context, name string) (AppInspectResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.appClient.InspectApp(ctx, &deployerv1.InspectAppRequest{Name: name})
	if err != nil {
		return AppInspectResult{}, DecodeRPCError(err)
	}
	app, err := appInfo(response.GetApp())
	if err != nil {
		return AppInspectResult{}, err
	}
	result := AppInspectResult{
		App:         app,
		Deployments: make([]DeploymentInfo, 0, len(response.GetDeployments())),
	}
	for _, deployment := range response.GetDeployments() {
		result.Deployments = append(result.Deployments, deploymentInfo(deployment))
	}
	for _, route := range response.GetRoutes() {
		result.Routes = append(result.Routes, routeInfo(route))
	}
	return result, nil
}

func (c *PlatformClient) GetAppStatus(ctx context.Context, name string) (AppStatusResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)
	response, err := c.appClient.GetAppStatus(ctx, &deployerv1.GetAppStatusRequest{Name: name})
	if err != nil {
		return AppStatusResult{}, DecodeRPCError(err)
	}
	app, err := appInfo(response.GetApp())
	if err != nil {
		return AppStatusResult{}, err
	}
	result := AppStatusResult{
		App:               app,
		LatestDeployment:  deploymentInfo(response.GetLatestDeployment()),
		RuntimeStatus:     response.GetRuntimeStatus(),
		DesiredReplicas:   int(response.GetDesiredReplicas()),
		AvailableReplicas: int(response.GetAvailableReplicas()),
		RunningNodes:      append([]string(nil), response.GetRunningNodes()...),
		Warnings:          append([]string(nil), response.GetWarnings()...),
	}
	for _, route := range response.GetRoutes() {
		result.Routes = append(result.Routes, routeInfo(route))
	}
	return result, nil
}

func (c *PlatformClient) ListRoutes(ctx context.Context) ([]RouteInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.appClient.ListRoutes(ctx, &deployerv1.ListRoutesRequest{})
	if err != nil {
		return nil, DecodeRPCError(err)
	}
	routes := make([]RouteInfo, 0, len(response.GetRoutes()))
	for _, route := range response.GetRoutes() {
		routes = append(routes, routeInfo(route))
	}
	return routes, nil
}

func (c *PlatformClient) InspectRoute(ctx context.Context, domain string) (RouteInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.appClient.InspectRoute(ctx, &deployerv1.InspectRouteRequest{Domain: domain})
	if err != nil {
		return RouteInfo{}, DecodeRPCError(err)
	}
	return routeInfo(response.GetRoute()), nil
}

func (c *PlatformClient) SetSecret(ctx context.Context, appName string, name string, value string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	_, err := c.secretClient.SetSecret(ctx, &deployerv1.SetSecretRequest{AppName: appName, Name: name, Value: value})
	return DecodeRPCError(err)
}

func (c *PlatformClient) ListSecrets(ctx context.Context, appName string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	response, err := c.secretClient.ListSecrets(ctx, &deployerv1.ListSecretsRequest{AppName: appName})
	if err != nil {
		return nil, DecodeRPCError(err)
	}
	return append([]string(nil), response.GetNames()...), nil
}

func (c *PlatformClient) DeleteSecret(ctx context.Context, appName string, name string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)

	_, err := c.secretClient.DeleteSecret(ctx, &deployerv1.DeleteSecretRequest{AppName: appName, Name: name})
	return DecodeRPCError(err)
}

func (c *PlatformClient) ListEvents(ctx context.Context, filter EventFilter) ([]EventInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = c.withBearer(ctx)
	response, err := c.eventClient.ListEvents(ctx, eventListRequest(filter))
	if err != nil {
		return nil, DecodeRPCError(err)
	}
	events := make([]EventInfo, 0, len(response.GetEvents()))
	for _, event := range response.GetEvents() {
		events = append(events, eventInfo(event))
	}
	return events, nil
}

func (c *PlatformClient) WatchEvents(ctx context.Context, filter EventFilter, receive func(EventInfo) error) error {
	stream, err := c.eventClient.WatchEvents(c.withBearer(ctx), &deployerv1.WatchEventsRequest{
		App:      filter.App,
		Node:     filter.Node,
		Type:     filter.Type,
		Severity: filter.Severity,
		Since:    eventSince(filter.Since),
	})
	if err != nil {
		return DecodeRPCError(err)
	}
	for {
		response, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return DecodeRPCError(err)
		}
		if err := receive(eventInfo(response.GetEvent())); err != nil {
			return err
		}
	}
}

func eventListRequest(filter EventFilter) *deployerv1.ListEventsRequest {
	return &deployerv1.ListEventsRequest{
		App:      filter.App,
		Node:     filter.Node,
		Type:     filter.Type,
		Severity: filter.Severity,
		Since:    eventSince(filter.Since),
		Limit:    int32(filter.Limit),
	}
}

func eventSince(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	return since.UTC().Format(time.RFC3339Nano)
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
		ID:                 node.GetId(),
		Name:               node.GetName(),
		Status:             node.GetStatus(),
		Labels:             node.GetLabels(),
		CreatedAt:          node.GetCreatedAt(),
		UpdatedAt:          node.GetUpdatedAt(),
		LastSeenAt:         node.GetLastSeenAt(),
		Hostname:           node.GetHostname(),
		Arch:               node.GetArch(),
		OS:                 node.GetOs(),
		Kernel:             node.GetKernel(),
		WireGuardIP:        node.GetWireguardIp(),
		WireGuardPublicKey: node.GetWireguardPublicKey(),
		KubernetesStatus:   node.GetKubernetesStatus(),
		KubernetesMessage:  node.GetKubernetesMessage(),
		Schedulable:        node.GetSchedulable(),
	}
}

func appInfo(app *deployerv1.App) (AppInfo, error) {
	if app == nil {
		return AppInfo{}, nil
	}
	cfg, err := appconfig.FromJSON(app.GetDesiredState())
	if err != nil {
		return AppInfo{}, fmt.Errorf("decode desired state for app %q: %w", app.GetName(), err)
	}
	return AppInfo{
		ID:           app.GetId(),
		Name:         app.GetName(),
		Image:        app.GetImage(),
		Replicas:     int(app.GetReplicas()),
		Domain:       cfg.Routing.Domain,
		StateMode:    cfg.State.Mode,
		DesiredState: cfg,
		CreatedAt:    app.GetCreatedAt(),
		UpdatedAt:    app.GetUpdatedAt(),
	}, nil
}

func deploymentInfo(deployment *deployerv1.Deployment) DeploymentInfo {
	if deployment == nil {
		return DeploymentInfo{}
	}
	return DeploymentInfo{
		ID:            deployment.GetId(),
		AppID:         deployment.GetAppId(),
		Status:        deployment.GetStatus(),
		FailureReason: deployment.GetFailureReason(),
		CreatedAt:     deployment.GetCreatedAt(),
		UpdatedAt:     deployment.GetUpdatedAt(),
	}
}

func routeInfo(route *deployerv1.Route) RouteInfo {
	if route == nil {
		return RouteInfo{}
	}
	return RouteInfo{
		ID:         route.GetId(),
		AppID:      route.GetAppId(),
		Domain:     route.GetDomain(),
		TargetPort: int(route.GetTargetPort()),
		Status:     route.GetStatus(),
		TLSEnabled: route.GetTlsEnabled(),
	}
}

func eventInfo(event *deployerv1.Event) EventInfo {
	if event == nil {
		return EventInfo{}
	}
	metadata := json.RawMessage(event.GetMetadataJson())
	if !json.Valid(metadata) {
		metadata = json.RawMessage(`{}`)
	}
	return EventInfo{
		ID:           event.GetId(),
		CreatedAt:    event.GetCreatedAt(),
		Type:         event.GetType(),
		Severity:     event.GetSeverity(),
		Message:      event.GetMessage(),
		AppID:        event.GetAppId(),
		NodeID:       event.GetNodeId(),
		DeploymentID: event.GetDeploymentId(),
		Metadata:     metadata,
	}
}

func NormalizeServerTarget(raw string) (string, error) {
	target, _, err := dialTargetAndCredentials(raw)
	return target, err
}

func dialTargetAndCredentials(raw string) (string, credentials.TransportCredentials, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, fmt.Errorf("server URL is required; pass --server or run deployer login <server-url>")
	}
	if !strings.Contains(raw, "://") {
		if strings.Contains(raw, "/") {
			return "", nil, fmt.Errorf("invalid server URL %q: expected host:port or http(s)://host:port", raw)
		}
		return raw, insecure.NewCredentials(), nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", nil, fmt.Errorf("invalid server URL %q: %w", raw, err)
	}

	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", nil, fmt.Errorf("invalid server URL %q: expected http or https", raw)
	}

	if parsed.Host == "" {
		return "", nil, fmt.Errorf("invalid server URL %q: missing host", raw)
	}
	if parsed.Scheme == "http" {
		return parsed.Host, insecure.NewCredentials(), nil
	}
	return parsed.Host, credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: parsed.Hostname(),
	}), nil
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
	case codes.FailedPrecondition:
		return fmt.Errorf("cannot proceed: %s", rpcStatus.Message())
	case codes.Unavailable:
		return fmt.Errorf("control plane unavailable: %s", rpcStatus.Message())
	default:
		return fmt.Errorf("control plane returned %s: %s", strings.ToLower(rpcStatus.Code().String()), rpcStatus.Message())
	}
}
