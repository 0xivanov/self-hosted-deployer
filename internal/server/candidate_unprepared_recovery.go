package server

import (
	"context"
	"errors"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type UnpreparedCandidateRecoveryRuntime interface {
	CandidateRequestRuntime
	EnsureCandidateRecoveryNamespace(context.Context) error
	CreateInactiveCandidateService(context.Context, appconfig.Config, string) error
}

var _ UnpreparedCandidateRecoveryRuntime = (*ingress.Controller)(nil)

// prepareUnstartedRecovery saves an intent for a bound request which never
// reached runtime activation preparation. Only passive bootstrap is allowed.
// The caller holds the app mutation lock; normal recovery subsequently fences
// traffic, retires the candidate identity and verifies the predecessor.
func (s AppService) prepareUnstartedRecovery(ctx context.Context, record domain.DeployRequest, cfg appconfig.Config, runtime UnpreparedCandidateRecoveryRuntime) error {
	if cfg.Name != record.AppName || cfg.Validate() != nil || cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode || (record.PreviousAppID == "") != (record.PreviousState == "") {
		return errors.New("invalid unprepared candidate recovery")
	}
	gate, target, err := runtime.CaptureActivationGate(ctx, cfg.Name)
	if apierrors.IsNotFound(err) && record.PreviousAppID == "" {
		if err = runtime.EnsureCandidateRecoveryNamespace(ctx); err != nil {
			return err
		}
		if err = runtime.CreateInactiveCandidateService(ctx, cfg, record.RequestID); err != nil {
			return err
		}
		gate, target, err = runtime.CaptureActivationGate(ctx, cfg.Name)
	}
	if err != nil {
		return err
	}
	if !validInitialCapture(gate, target, cfg.Name) {
		return errors.New("invalid recovery target")
	}
	if record.PreviousAppID == "" {
		if !ingress.IsInactiveCandidateTarget(cfg, record.RequestID, target) {
			return errors.New("initial recovery target is not inactive")
		}
	} else {
		previous, decodeErr := appconfig.FromJSON(record.PreviousState)
		if decodeErr != nil || previous.Validate() != nil || previous.Name != cfg.Name {
			return errors.New("invalid recovery predecessor")
		}
		ready, readyErr := runtime.RestoredCandidateTargetReady(ctx, gate, target, previous)
		if readyErr != nil {
			return readyErr
		}
		if !ready {
			return errors.New("recovery predecessor is not ready")
		}
	}
	intent := candidateOperationIntent{AppName: record.AppName, RequestID: record.RequestID, RequestedState: record.RequestedState, SecretRevision: cfg.EnvironmentRevision, RegistrySecret: ingress.CandidateRegistrySecretName(cfg), InitialGate: gate, PredecessorTarget: target}
	encoded, err := canonicalCandidateJSON(intent)
	if err != nil {
		return err
	}
	return s.candidateCheckpoints.SaveActivationIntent(ctx, record.AppName, record.RequestID, encoded)
}
