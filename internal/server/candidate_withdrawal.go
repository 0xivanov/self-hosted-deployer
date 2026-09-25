package server

import (
	"context"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type candidateWithdrawalReader interface {
	WithdrawalRequested(context.Context, string, string) (bool, error)
}
type candidateWithdrawalCreator interface {
	candidateWithdrawalReader
	BeginWithdrawal(context.Context, domain.DeployRequest, string, string, time.Time) (domain.CandidateBinding, bool, error)
}

func (s AppService) rejectRequestedWithdrawal(ctx context.Context, app, requestID string) error {
	if reader, ok := s.candidateBindings.(candidateWithdrawalReader); ok {
		withdrawal, err := reader.WithdrawalRequested(ctx, app, requestID)
		if err != nil {
			return status.Error(codes.Internal, "read deployment withdrawal decision")
		}
		if withdrawal {
			return status.Error(codes.FailedPrecondition, "deployment withdrawal was requested; recover this request instead of activating it")
		}
	}
	return nil
}

// WithdrawDeployRequest records the original request and an irrevocable
// no-activation decision atomically, including when the initial submission has
// not arrived. No dependency resolution, capacity admission or workload start
// occurs. A late submit observes the withdrawal decision under the same app lock.
func (s AppService) WithdrawDeployRequest(ctx context.Context, req *deployerv1.DeployAppRequest) (*deployerv1.DeployRequestMetadata, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if !registryauth.ValidRevision(req.GetRequestId()) || !req.GetReportWithdrawal() {
		return nil, status.Error(codes.InvalidArgument, "valid request_id and report_withdrawal are required")
	}
	cfg, err := appconfig.Parse([]byte(req.GetDeployerYaml()))
	if err != nil || cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode || len(cfg.Secrets) != 0 {
		return nil, status.Error(codes.InvalidArgument, "withdrawal requires the original hosted stateless deployment configuration")
	}
	creator, supported := s.candidateBindings.(candidateWithdrawalCreator)
	runtime, runtimeSupported := s.runtime.(UnpreparedCandidateRecoveryRuntime)
	if !s.enableCandidateOperations || !supported || !runtimeSupported || s.deploymentRequests == nil || s.candidateCheckpoints == nil || s.candidateFinalizer == nil {
		return nil, status.Error(codes.FailedPrecondition, "candidate withdrawal is disabled or not configured")
	}
	release, err := s.acquireOperation(ctx, cfg.Name)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := s.checkCandidateDeletion(ctx, cfg.Name); err != nil {
		return nil, err
	}
	requested, err := cfg.JSON()
	if err != nil {
		return nil, status.Error(codes.Internal, "encode withdrawal configuration")
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
	_, _, err = creator.BeginWithdrawal(ctx, domain.DeployRequest{AppName: cfg.Name, RequestID: req.GetRequestId(), State: "pending", RequestedState: requested, ReportWithdrawal: true, CreatedAt: now, UpdatedAt: now}, appID, deploymentID, now)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "withdrawal conflicts with an existing deployment or request")
	}
	lookup := &deployerv1.GetDeployRequestRequest{AppName: cfg.Name, RequestId: req.GetRequestId()}
	record, err := s.deploymentRequests.Find(ctx, cfg.Name, req.GetRequestId())
	if err != nil {
		return nil, status.Error(codes.Internal, "read withdrawal request")
	}
	if record.State != "pending" {
		// Applied means the deployment won the race. Never report withdrawal or
		// reverse an already committed release.
		return s.GetDeployRequest(ctx, lookup)
	}
	return s.mutateCandidateRequestLocked(ctx, lookup, true, runtime)
}
