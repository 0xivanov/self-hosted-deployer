package server

import (
	"context"
	"errors"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

var _ CandidateRetirementRuntime = (*ingress.Controller)(nil)

type CandidateRetirementRuntime interface {
	CandidateRecoveryRuntime
	RetireCandidateDeployment(context.Context, appconfig.Config, string, string, string) error
	CandidateRetired(context.Context, appconfig.Config, string, string, string) (bool, error)
	RestoredCandidateTargetReady(context.Context, ingress.ActivationGate, ingress.ActivationTarget, appconfig.Config) (bool, error)
}

// RecoverCandidateOperation restores traffic, pins the abandoned generation at
// zero replicas, observes pod removal and checks the previous release. True is
// evidence for the caller's application/route bookkeeping, not a completed
// deployment request or public HTTPS guarantee. Hold the app mutation lock.
func RecoverCandidateOperation(ctx context.Context, repo RuntimeCheckpointRepository, requests DeploymentRequestRepository, runtime CandidateRetirementRuntime, app, requestID string) (bool, error) {
	if repo == nil || requests == nil || runtime == nil {
		return false, errors.New("candidate recovery runtime unavailable")
	}
	request, err := requests.Find(ctx, app, requestID)
	if err != nil {
		return false, err
	}
	cfg, err := appconfig.FromJSON(request.RequestedState)
	if err != nil || cfg.Validate() != nil || cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode || request.AppName != app || request.RequestID != requestID || request.State != "pending" || cfg.Name != app {
		return false, errors.New("candidate recovery request is invalid")
	}
	var previous appconfig.Config
	if (request.PreviousAppID == "") != (request.PreviousState == "") {
		return false, errors.New("candidate predecessor is incomplete")
	}
	if request.PreviousState != "" {
		previous, err = appconfig.FromJSON(request.PreviousState)
		if err != nil || previous.Validate() != nil || previous.Name != app {
			return false, errors.New("candidate predecessor is invalid")
		}
	}
	if request.PreviousAppID == "" {
		checkpoint, readErr := repo.FindRuntimeCheckpoint(ctx, app, requestID)
		if readErr != nil {
			return false, readErr
		}
		intent, decodeErr := decodeCandidateIntent(checkpoint.IntentJSON)
		if decodeErr != nil || !ingress.IsInactiveCandidateTarget(cfg, requestID, intent.PredecessorTarget) {
			return false, errors.New("initial candidate target was not inactive")
		}
	}
	gate, err := RestoreCandidateTraffic(ctx, repo, requests, runtime, app, requestID)
	if err != nil {
		return false, err
	}
	pullSecret := ingress.CandidateRegistrySecretName(cfg)
	if err = runtime.RetireCandidateDeployment(ctx, cfg, cfg.EnvironmentRevision, requestID, pullSecret); err != nil {
		return false, err
	}
	retired, err := runtime.CandidateRetired(ctx, cfg, cfg.EnvironmentRevision, requestID, pullSecret)
	if err != nil || !retired {
		return false, err
	}
	checkpoint, err := repo.FindRuntimeCheckpoint(ctx, app, requestID)
	if err != nil {
		return false, err
	}
	intent, err := decodeCandidateIntent(checkpoint.IntentJSON)
	if err != nil {
		return false, err
	}
	current, target, err := runtime.CaptureActivationGate(ctx, app)
	if err != nil {
		return false, err
	}
	if current != gate || !apiequality.Semantic.DeepEqual(target, intent.PredecessorTarget) {
		return false, errors.New("restored traffic changed during retirement")
	}
	if request.PreviousAppID == "" {
		return true, nil
	}
	return runtime.RestoredCandidateTargetReady(ctx, gate, intent.PredecessorTarget, previous)
}
