package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

// MutationAuditRecord contains only operational metadata. It intentionally
// has no request, response, or error text fields because those may contain
// credentials or customer configuration.
type MutationAuditRecord struct {
	Timestamp     time.Time         `json:"timestamp"`
	CorrelationID string            `json:"correlation_id"`
	TokenID       string            `json:"token_id"`
	CallerKind    CallerKind        `json:"caller_kind"`
	NodeID        string            `json:"node_id,omitempty"`
	Method        string            `json:"method"`
	Target        map[string]string `json:"target,omitempty"`
	Outcome       codes.Code        `json:"outcome"`
}

type MutationAuditSink interface {
	RecordMutation(MutationAuditRecord) error
}

type slogMutationAuditSink struct{ logger *slog.Logger }

func (s slogMutationAuditSink) RecordMutation(record MutationAuditRecord) error {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("mutation audit",
		"timestamp", record.Timestamp.UTC().Format(time.RFC3339Nano),
		"correlation_id", record.CorrelationID,
		"token_id", record.TokenID,
		"caller_kind", string(record.CallerKind),
		"node_id", record.NodeID,
		"method", record.Method,
		"target", record.Target,
		"outcome", record.Outcome.String(),
	)
	return nil
}

func newSlogMutationAuditSink(logger *slog.Logger) MutationAuditSink {
	return slogMutationAuditSink{logger: logger}
}

type mutationAuditSinkFunc func(MutationAuditRecord) error

func (f mutationAuditSinkFunc) RecordMutation(record MutationAuditRecord) error { return f(record) }

// NewMutationAuditInterceptor records authenticated mutating RPCs after the
// auth interceptor has attached Caller to the context. Sink failures are
// deliberately ignored so audit availability cannot change API behavior.
func NewMutationAuditInterceptor(sink MutationAuditSink) grpc.UnaryServerInterceptor {
	return newMutationAuditInterceptor(sink, time.Now, newCorrelationID)
}

func newMutationAuditInterceptor(sink MutationAuditSink, now func() time.Time, correlationID func() string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !isMutationMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		caller, ok := CallerFromContext(ctx)
		if !ok {
			return handler(ctx, req)
		}
		requestID := correlationID()
		ctx = WithRequestID(ctx, requestID)
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", requestID))
		resp, err := handler(ctx, req)
		if sink != nil {
			_ = sink.RecordMutation(MutationAuditRecord{
				Timestamp:     now().UTC(),
				CorrelationID: requestID,
				TokenID:       caller.TokenID,
				CallerKind:    caller.Kind,
				NodeID:        caller.NodeID,
				Method:        info.FullMethod,
				Target:        mutationTarget(info.FullMethod, req),
				Outcome:       status.Code(err),
			})
		}
		return resp, err
	}
}

func newCorrelationID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	// crypto/rand failure must not turn a completed mutation into an API error.
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func isMutationMethod(method string) bool {
	switch method {
	case "/deployer.v1.AppService/DeployApp",
		"/deployer.v1.AppService/DeleteApp",
		"/deployer.v1.NodeService/CreateJoinToken",
		"/deployer.v1.NodeService/DrainNode",
		"/deployer.v1.NodeService/UncordonNode",
		"/deployer.v1.NodeService/RemoveNode",
		"/deployer.v1.NodeService/PurgeNode",
		"/deployer.v1.NodeService/RenameNode",
		"/deployer.v1.SecretService/SetSecret",
		"/deployer.v1.SecretService/DeleteSecret":
		return true
	default:
		return false
	}
}

var auditIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

func mutationTarget(method string, req any) map[string]string {
	target := make(map[string]string)
	put := func(key, value string) {
		value = strings.TrimSpace(value)
		if auditIdentifierPattern.MatchString(value) {
			target[key] = value
		}
	}
	switch method {
	case "/deployer.v1.AppService/DeployApp":
		if request, ok := req.(*deployerv1.DeployAppRequest); ok {
			var parsed struct {
				Name string `yaml:"name" json:"name"`
			}
			if yaml.Unmarshal([]byte(request.GetDeployerYaml()), &parsed) == nil {
				put("app", parsed.Name)
			}
		}
	case "/deployer.v1.AppService/DeleteApp":
		if request, ok := req.(*deployerv1.DeleteAppRequest); ok {
			put("app", request.GetName())
		}
	case "/deployer.v1.NodeService/CreateJoinToken":
		if request, ok := req.(*deployerv1.CreateJoinTokenRequest); ok {
			put("node", request.GetNodeName())
		}
	case "/deployer.v1.NodeService/DrainNode":
		if request, ok := req.(*deployerv1.DrainNodeRequest); ok {
			put("node", request.GetNodeRef())
		}
	case "/deployer.v1.NodeService/UncordonNode":
		if request, ok := req.(*deployerv1.UncordonNodeRequest); ok {
			put("node", request.GetNodeRef())
		}
	case "/deployer.v1.NodeService/RemoveNode":
		if request, ok := req.(*deployerv1.RemoveNodeRequest); ok {
			put("node", request.GetNodeRef())
		}
	case "/deployer.v1.NodeService/PurgeNode":
		if request, ok := req.(*deployerv1.PurgeNodeRequest); ok {
			put("node", request.GetNodeRef())
		}
	case "/deployer.v1.NodeService/RenameNode":
		if request, ok := req.(*deployerv1.RenameNodeRequest); ok {
			put("node", request.GetNodeRef())
			put("new_name", request.GetNewName())
		}
	case "/deployer.v1.SecretService/SetSecret":
		if request, ok := req.(*deployerv1.SetSecretRequest); ok {
			put("app", request.GetAppName())
			put("secret", request.GetName())
		}
	case "/deployer.v1.SecretService/DeleteSecret":
		if request, ok := req.(*deployerv1.DeleteSecretRequest); ok {
			put("app", request.GetAppName())
			put("secret", request.GetName())
		}
	}
	if len(target) == 0 {
		return nil
	}
	return target
}
