package server

import (
	"context"
	"errors"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CandidateBindingReader interface {
	Find(context.Context, string, string) (domain.CandidateBinding, error)
}

type CandidateRequestRuntime interface {
	CandidateOperationRuntime
	CandidateCompletionRuntime
}

// AdvanceDeployRequest advances an already prepared, bound candidate. It cannot
// turn an ambiguous legacy request into a candidate or recreate lost intent.
func (s AppService) AdvanceDeployRequest(ctx context.Context, req *deployerv1.GetDeployRequestRequest) (*deployerv1.DeployRequestMetadata, error) {
	return s.mutateCandidateRequest(ctx, req, false)
}

// RecoverDeployRequest explicitly abandons a candidate and restores its saved
// predecessor. It never submits a replacement deployment.
func (s AppService) RecoverDeployRequest(ctx context.Context, req *deployerv1.GetDeployRequestRequest) (*deployerv1.DeployRequestMetadata, error) {
	return s.mutateCandidateRequest(ctx, req, true)
}

func (s AppService) mutateCandidateRequest(ctx context.Context, req *deployerv1.GetDeployRequestRequest, recover bool) (*deployerv1.DeployRequestMetadata, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetAppName()) == "" || !registryauth.ValidRevision(req.GetRequestId()) {
		return nil, status.Error(codes.InvalidArgument, "app_name and valid request_id are required")
	}
	if !s.enableCandidateOperations || s.deploymentRequests == nil || s.candidateBindings == nil || s.candidateCheckpoints == nil || s.candidateFinalizer == nil {
		return nil, status.Error(codes.FailedPrecondition, "candidate operations are disabled or not configured")
	}
	runtime, ok := s.runtime.(CandidateRequestRuntime)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "candidate operations are unsupported by this runtime")
	}
	release, err := s.acquireOperation(ctx, req.GetAppName())
	if err != nil {
		return nil, err
	}
	defer release()
	binding, err := s.candidateBindings.Find(ctx, req.GetAppName(), req.GetRequestId())
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.FailedPrecondition, "request is not a prepared candidate")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "read candidate binding")
	}
	if binding.AppName != req.GetAppName() || binding.RequestID != req.GetRequestId() || binding.AppID == "" || binding.DeploymentID == "" {
		return nil, status.Error(codes.FailedPrecondition, "candidate binding is invalid")
	}
	request, err := s.deploymentRequests.Find(ctx, req.GetAppName(), req.GetRequestId())
	if err != nil {
		return nil, status.Error(codes.Internal, "read candidate request")
	}
	if request.AppName != req.GetAppName() || request.RequestID != req.GetRequestId() {
		return nil, status.Error(codes.FailedPrecondition, "candidate request identity is invalid")
	}
	outcome := "applied"
	if recover {
		outcome = "withdrawn"
	}
	if request.State != "pending" {
		if request.State != outcome {
			return nil, status.Error(codes.FailedPrecondition, "request already completed with a different outcome")
		}
		return s.GetDeployRequest(ctx, req)
	}
	cfg, decodeErr := appconfig.FromJSON(request.RequestedState)
	if decodeErr != nil || cfg.Validate() != nil {
		return nil, status.Error(codes.FailedPrecondition, "candidate configuration is invalid")
	}
	checkpoint, checkpointErr := s.candidateCheckpoints.FindRuntimeCheckpoint(ctx, request.AppName, request.RequestID)
	if !recover && errors.Is(checkpointErr, db.ErrNotFound) {
		preparer, ok := s.runtime.(CandidateSubmissionRuntime)
		if !ok {
			return nil, status.Error(codes.FailedPrecondition, "candidate preparation is unsupported")
		}
		if err = s.prepareCandidateRequest(ctx, request, cfg, preparer); err != nil {
			return nil, err
		}
	} else if checkpointErr != nil {
		return nil, status.Error(codes.FailedPrecondition, "candidate preparation is incomplete; operator review required")
	} else if checkpoint.AppName != request.AppName || checkpoint.RequestID != request.RequestID {
		return nil, status.Error(codes.FailedPrecondition, "candidate checkpoint identity is invalid")
	}
	if !recover {
		if checkpointErr == nil && checkpoint.Stage != "fenced" && checkpoint.Stage != "activated" {
			return nil, status.Error(codes.FailedPrecondition, "candidate cannot advance from this stage; recovery or operator review required")
		}
		_, err = AdvanceCandidateOperation(ctx, s.candidateCheckpoints, s.deploymentRequests, runtime, request.AppName, request.RequestID, cfg, cfg.EnvironmentRevision, ingress.CandidateRegistrySecretName(cfg))
		if errors.Is(err, ingress.ErrCandidateNotReady) {
			return s.GetDeployRequest(ctx, req)
		}
		if err != nil {
			return nil, status.Error(codes.FailedPrecondition, "candidate could not advance; inspect request or recover")
		}
	}
	_, err = CompleteCandidateOperation(ctx, s.candidateCheckpoints, s.deploymentRequests, s.candidateFinalizer, runtime, request.AppName, request.RequestID, outcome, s.routeTLSEnabled, s.now().UTC())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "candidate outcome remains unconfirmed; inspect request before retrying")
	}
	return s.GetDeployRequest(ctx, req)
}
