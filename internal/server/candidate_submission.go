package server

import (
	"context"
	"errors"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type CandidateRequestCreator interface {
	Begin(context.Context, domain.DeployRequest, string, string, time.Time) (domain.CandidateBinding, bool, error)
}
type CandidateSubmissionRuntime interface {
	CandidateRequestRuntime
	PrepareCandidateDependencies(context.Context, appconfig.Config, map[string]string, string, string, *registryauth.Credential) (string, error)
	CreateInactiveCandidateService(context.Context, appconfig.Config, string) error
}

var _ CandidateSubmissionRuntime = (*ingress.Controller)(nil)

// deployCandidateApp runs under DeployApp's app mutation lock. Request creation
// and binding happen in one transaction before any Kubernetes write.
func (s AppService) deployCandidateApp(ctx context.Context, cfg appconfig.Config, req *deployerv1.DeployAppRequest) (*deployerv1.DeployAppResponse, error) {
	creator, ok := s.candidateBindings.(CandidateRequestCreator)
	runtime, runtimeOK := s.runtime.(CandidateSubmissionRuntime)
	if !ok || !runtimeOK || s.deploymentRequests == nil || s.candidateCheckpoints == nil || s.candidateFinalizer == nil {
		return nil, status.Error(codes.FailedPrecondition, "candidate submission is not configured")
	}
	if cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode || len(cfg.Secrets) > 0 {
		return nil, status.Error(codes.InvalidArgument, "candidate submission requires hosted stateless configuration and versioned environment settings")
	}
	requested, err := cfg.JSON()
	if err != nil {
		return nil, status.Error(codes.Internal, "encode candidate configuration")
	}
	// Reject missing credentials/environment and known capacity failures before
	// accepting a new request. Existing requests retain their durable outcome.
	_, lookupErr := s.deploymentRequests.Find(ctx, cfg.Name, req.GetRequestId())
	if errors.Is(lookupErr, db.ErrNotFound) {
		if cfg.EnvironmentRevision != "" {
			if _, err := resolveEnvironmentBundle(ctx, s.environmentBundles, s.cipher, cfg.Name, cfg.EnvironmentRevision); err != nil {
				return nil, err
			}
		}
		if _, err := resolveRuntimeRegistry(ctx, s.runtime, s.registryCredentials, cfg); err != nil {
			return nil, err
		}
		if err := preflightHosting(ctx, s.runtime, cfg); err != nil {
			return nil, err
		}
	} else if lookupErr != nil {
		return nil, status.Error(codes.Internal, "read candidate request")
	}
	appID, err := newID("app")
	if err != nil {
		return nil, err
	}
	deploymentID, err := newID("deploy")
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	binding, _, err := creator.Begin(ctx, domain.DeployRequest{AppName: cfg.Name, RequestID: req.GetRequestId(), State: "pending", RequestedState: requested, ReportWithdrawal: req.GetReportWithdrawal(), CreatedAt: now, UpdatedAt: now}, appID, deploymentID, now)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "candidate request conflicts with an existing request or app")
	}
	record, err := s.deploymentRequests.Find(ctx, cfg.Name, req.GetRequestId())
	if err != nil {
		return nil, status.Error(codes.Internal, "read candidate request")
	}
	if record.State != "pending" {
		return decodeCandidateReply(record.ResponseJSON)
	}
	if err = s.prepareCandidateRequest(ctx, record, cfg, runtime); err != nil {
		return nil, err
	}
	_, err = AdvanceCandidateOperation(ctx, s.candidateCheckpoints, s.deploymentRequests, runtime, cfg.Name, req.GetRequestId(), cfg, cfg.EnvironmentRevision, ingress.CandidateRegistrySecretName(cfg))
	if err != nil && !errors.Is(err, ingress.ErrCandidateNotReady) {
		return nil, status.Error(codes.FailedPrecondition, "candidate request remains pending; inspect or recover before retrying")
	}
	if err == nil {
		result, completeErr := CompleteCandidateOperation(ctx, s.candidateCheckpoints, s.deploymentRequests, s.candidateFinalizer, runtime, cfg.Name, req.GetRequestId(), "applied", s.routeTLSEnabled, s.now().UTC())
		if completeErr != nil {
			return nil, status.Error(codes.FailedPrecondition, "candidate outcome remains unconfirmed; inspect request before retrying")
		}
		if result != nil {
			return result, nil
		}
	}
	app, err := s.apps.FindByID(ctx, binding.AppID)
	if err != nil {
		return nil, status.Error(codes.Internal, "read pending candidate app")
	}
	appProto, err := protoApp(app)
	if err != nil {
		return nil, err
	}
	return &deployerv1.DeployAppResponse{App: appProto, Deployment: &deployerv1.Deployment{Id: binding.DeploymentID, AppId: binding.AppID, Status: deploymentStatusPending, CreatedAt: formatProtoTime(binding.CreatedAt), UpdatedAt: formatProtoTime(binding.CreatedAt)}, RequestedState: requested}, nil
}

// Missing intent is safe to resume only for a request atomically bound by the
// candidate creator. Preparation writes immutable dependencies and a create-only
// inactive Service. It never creates candidate pods or selects traffic.
func (s AppService) prepareCandidateRequest(ctx context.Context, record domain.DeployRequest, cfg appconfig.Config, runtime CandidateSubmissionRuntime) error {
	_, err := s.candidateCheckpoints.FindRuntimeCheckpoint(ctx, record.AppName, record.RequestID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return status.Error(codes.Internal, "read candidate preparation")
	}
	var values map[string]string
	if cfg.EnvironmentRevision != "" {
		values, err = resolveEnvironmentBundle(ctx, s.environmentBundles, s.cipher, cfg.Name, cfg.EnvironmentRevision)
		if err != nil {
			return err
		}
	}
	credential, err := resolveRuntimeRegistry(ctx, s.runtime, s.registryCredentials, cfg)
	if err != nil {
		return err
	}
	if _, err = runtime.PrepareCandidateDependencies(ctx, cfg, values, cfg.EnvironmentRevision, record.RequestID, credential); err != nil {
		return status.Error(codes.FailedPrecondition, "candidate dependencies could not be prepared; request remains pending")
	}
	gate, target, err := runtime.CaptureActivationGate(ctx, cfg.Name)
	if apierrors.IsNotFound(err) && record.PreviousAppID == "" {
		if err = runtime.CreateInactiveCandidateService(ctx, cfg, record.RequestID); err != nil {
			return status.Error(codes.FailedPrecondition, "candidate bootstrap unconfirmed; inspect request before retrying")
		}
		gate, target, err = runtime.CaptureActivationGate(ctx, cfg.Name)
	}
	if err != nil {
		return status.Error(codes.FailedPrecondition, "candidate traffic target is unavailable")
	}
	if !validInitialCapture(gate, target, cfg.Name) {
		return status.Error(codes.FailedPrecondition, "candidate traffic target is invalid")
	}
	if record.PreviousAppID == "" && !ingress.IsInactiveCandidateTarget(cfg, record.RequestID, target) {
		return status.Error(codes.FailedPrecondition, "initial candidate service is not the saved request's inactive target")
	}
	if record.PreviousAppID != "" {
		previous, decodeErr := appconfig.FromJSON(record.PreviousState)
		if decodeErr != nil || previous.Validate() != nil || previous.Name != cfg.Name || previous.Service.Port != cfg.Service.Port {
			return status.Error(codes.FailedPrecondition, "candidate predecessor is invalid or port change is unsupported")
		}
	}
	return nil
}
