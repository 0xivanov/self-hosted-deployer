package server

import (
	"context"
	"errors"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

type candidateInitialReuseRuntime interface {
	CandidateRequestRuntime
	InitialCandidateRequest(context.Context, ingress.ActivationGate) (string, error)
	ResetInactiveCandidateService(context.Context, ingress.ActivationGate, appconfig.Config, string, string) (ingress.ActivationGate, ingress.ActivationTarget, error)
}

var _ candidateInitialReuseRuntime = (*ingress.Controller)(nil)

// reuseWithdrawnInitialCandidate admits exactly one safe retry after an
// initial candidate was withdrawn. It requires the old terminal request,
// binding, withdrawn checkpoint and exact restored gate, plus a zero-replica
// retirement proof. The Service is then changed through its resourceVersion
// gate, preserving its UID and fencing delayed writers from the old request.
func (s AppService) reuseWithdrawnInitialCandidate(ctx context.Context, record domain.DeployRequest, cfg appconfig.Config, gate ingress.ActivationGate, target ingress.ActivationTarget, runtime CandidateRequestRuntime) (ingress.ActivationGate, ingress.ActivationTarget, error) {
	reuse, ok := runtime.(candidateInitialReuseRuntime)
	if !ok {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("candidate bootstrap reuse is unsupported")
	}
	oldID, err := reuse.InitialCandidateRequest(ctx, gate)
	if err != nil || oldID == record.RequestID || !registryauth.ValidRevision(oldID) {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("inactive candidate bootstrap identity is ambiguous")
	}
	oldRequest, err := s.deploymentRequests.Find(ctx, record.AppName, oldID)
	if err != nil || oldRequest.AppName != record.AppName || oldRequest.RequestID != oldID || oldRequest.State != "withdrawn" || oldRequest.PreviousAppID != "" || oldRequest.PreviousState != "" {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("inactive candidate bootstrap was not withdrawn safely")
	}
	oldBinding, err := s.candidateBindings.Find(ctx, record.AppName, oldID)
	if err != nil || oldBinding.AppName != record.AppName || oldBinding.RequestID != oldID || oldBinding.AppID == "" || oldBinding.DeploymentID == "" {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("withdrawn candidate binding is unavailable")
	}
	newBinding, err := s.candidateBindings.Find(ctx, record.AppName, record.RequestID)
	if err != nil || newBinding.AppName != record.AppName || newBinding.RequestID != record.RequestID || newBinding.AppID != oldBinding.AppID {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("retry candidate binding does not reuse the withdrawn app")
	}
	if s.apps == nil {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("candidate app repository is unavailable")
	}
	oldApp, err := s.apps.FindByID(ctx, oldBinding.AppID)
	if err != nil || oldApp.ID != oldBinding.AppID || oldApp.Name != record.AppName || oldApp.DeletedAt == nil {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("withdrawn candidate app identity is not retired")
	}
	oldCfg, err := appconfig.FromJSON(oldRequest.RequestedState)
	if err != nil || oldCfg.Validate() != nil || oldCfg.Name != record.AppName || oldCfg.Hosting == nil || oldCfg.State.Mode != appconfig.DefaultStateMode {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("withdrawn candidate configuration is invalid")
	}
	checkpoint, err := s.candidateCheckpoints.FindRuntimeCheckpoint(ctx, record.AppName, oldID)
	if err != nil || checkpoint.Stage != "withdrawn" || checkpoint.AppName != record.AppName || checkpoint.RequestID != oldID {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("withdrawn candidate recovery proof is unavailable")
	}
	intent, err := decodeCandidateIntent(checkpoint.IntentJSON)
	if err != nil || !intentMatches(intent, record.AppName, oldID, oldRequest.RequestedState, oldCfg.EnvironmentRevision, ingress.CandidateRegistrySecretName(oldCfg)) || !validInitialCapture(intent.InitialGate, intent.PredecessorTarget, record.AppName) || !ingress.IsInactiveCandidateTarget(oldCfg, oldID, intent.PredecessorTarget) {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("withdrawn candidate recovery intent is invalid")
	}
	oldGate, err := decodeGate(checkpoint.GateJSON)
	if err != nil || oldGate != gate || !validSavedGate(oldGate, intent.InitialGate, record.AppName, candidateRecoveryID(record.AppName, oldID), true) || !apiequality.Semantic.DeepEqual(target, intent.PredecessorTarget) {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("withdrawn candidate traffic gate changed")
	}
	retired, err := reuse.CandidateRetired(ctx, oldCfg, oldCfg.EnvironmentRevision, oldID, ingress.CandidateRegistrySecretName(oldCfg))
	if err != nil || !retired {
		if err != nil {
			return ingress.ActivationGate{}, ingress.ActivationTarget{}, err
		}
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("withdrawn candidate workload is not retired")
	}
	newGate, newTarget, err := reuse.ResetInactiveCandidateService(ctx, gate, cfg, oldID, record.RequestID)
	if err != nil {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, err
	}
	if !validInitialCapture(newGate, newTarget, record.AppName) || !ingress.IsInactiveCandidateTarget(cfg, record.RequestID, newTarget) {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("candidate bootstrap retry target is invalid")
	}
	if newGate.UID != gate.UID || newGate.Namespace != gate.Namespace || newGate.App != gate.App || newGate.OperationID != ingress.CandidateBootstrapOperationID(record.AppName, record.RequestID) {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("candidate bootstrap retry fence is invalid")
	}
	return newGate, newTarget, nil
}
