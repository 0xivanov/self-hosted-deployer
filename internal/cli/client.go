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
	client  deployerv1.PlatformServiceClient
	token   string
	timeout time.Duration
}

type ServerStatus struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Ready     bool   `json:"ready"`
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

	return NewPlatformClientForService(deployerv1.NewPlatformServiceClient(conn), token), conn, nil
}

func NewPlatformClientForService(client deployerv1.PlatformServiceClient, token string) *PlatformClient {
	return &PlatformClient{
		client:  client,
		token:   token,
		timeout: defaultRequestTimeout,
	}
}

func (c *PlatformClient) Status(ctx context.Context) (ServerStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if c.token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
	}

	response, err := c.client.GetStatus(ctx, &deployerv1.GetStatusRequest{})
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
	case codes.Unavailable:
		return fmt.Errorf("control plane unavailable: %s", rpcStatus.Message())
	default:
		return fmt.Errorf("control plane returned %s: %s", strings.ToLower(rpcStatus.Code().String()), rpcStatus.Message())
	}
}
