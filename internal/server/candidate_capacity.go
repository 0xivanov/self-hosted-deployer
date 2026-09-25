package server

import (
	"context"
	"errors"
	"maps"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type candidateCapacityRuntime interface {
	CandidateRequestRuntime
	candidateDeletionGuard
	RetireLegacyCandidatePredecessor(context.Context, ingress.ActivationGate) (bool, error)
}

var _ candidateCapacityRuntime = (*ingress.Controller)(nil)

// Reclamation runs only AFTER the applied receipt is committed. Before that
// point recovery still needs its healthy predecessor. The mutating advance
// operation retries reclamation; request lookup stays read-only.
func (s AppService) reclaimCandidateCapacity(ctx context.Context, app, requestID string) error {
	runtime, supported := s.runtime.(candidateCapacityRuntime)
	if !supported { // Optional for non-Kubernetes adapters.
		return nil
	}
	history, ok := s.candidateBindings.(candidateGenerationReader)
	if !ok || s.apps == nil || s.deploymentRequests == nil {
		return status.Error(codes.FailedPrecondition, "candidate capacity cleanup is not configured")
	}
	fail := func() error {
		return status.Error(codes.FailedPrecondition, "release is applied; earlier workload cleanup is pending, retry advance")
	}
	request, err := s.deploymentRequests.Find(ctx, app, requestID)
	if err != nil {
		return fail()
	}
	if request.AppName != app || request.RequestID != requestID {
		return fail()
	}
	if request.State != "applied" {
		return nil
	}
	binding, err := s.candidateBindings.Find(ctx, app, requestID)
	if err != nil || binding.AppName != app || binding.RequestID != requestID {
		return fail()
	}
	current, err := s.apps.FindByID(ctx, binding.AppID)
	if err != nil {
		return fail()
	}
	if current.DeletedAt != nil || current.Name != app || current.DesiredStateJSON != request.RequestedState {
		return nil
	}
	// Another in-progress deployment may need the selected release as its
	// predecessor. Never retire anything on a stale advance request.
	if _, err = s.deploymentRequests.PendingByApp(ctx, app); err == nil {
		return nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return fail()
	}
	if err = runtime.CheckCandidateDeletion(ctx, app); err != nil {
		return fail()
	}
	gate, target, err := runtime.CaptureActivationGate(ctx, app)
	if err != nil {
		return fail()
	}
	selected, err := ingress.CandidateSelector(app, requestID)
	if err != nil {
		return fail()
	}
	if gate.OperationID != requestID || !maps.Equal(target.Selector, selected) {
		return nil
	}
	cfg, err := appconfig.FromJSON(request.RequestedState)
	if err != nil || cfg.Validate() != nil || cfg.Name != app {
		return fail()
	}
	ready, err := runtime.RestoredCandidateTargetReady(ctx, gate, target, cfg)
	if err != nil || !ready {
		return fail()
	}
	generations, err := history.GenerationsByApp(ctx, app, binding.AppID)
	if err != nil {
		return fail()
	}
	configs := make([]appconfig.Config, len(generations))
	selectedFound := false
	for i, generation := range generations {
		if generation.AppName != app || generation.AppID != binding.AppID || !registryauth.ValidRevision(generation.RequestID) || (generation.State != "applied" && generation.State != "withdrawn") {
			return fail()
		}
		configs[i], err = appconfig.FromJSON(generation.RequestedState)
		c := configs[i]
		if err != nil || c.Validate() != nil || c.Name != app || c.Hosting == nil || c.State.Mode != appconfig.DefaultStateMode || ingress.ValidateCandidateReferences(c, c.EnvironmentRevision, ingress.CandidateRegistrySecretName(c)) != nil {
			return fail()
		}
		if generation.RequestID == requestID {
			if generation.State != "applied" || generation.RequestedState != request.RequestedState {
				return fail()
			}
			selectedFound = true
		}
	}
	if !selectedFound {
		return fail()
	}
	for i, generation := range generations {
		if generation.RequestID == requestID {
			continue
		}
		observed, _, captureErr := runtime.CaptureActivationGate(ctx, app)
		if captureErr != nil || observed != gate {
			return fail()
		}
		c := configs[i]
		if err = runtime.RetireCandidateDeployment(ctx, c, c.EnvironmentRevision, generation.RequestID, ingress.CandidateRegistrySecretName(c)); err != nil {
			return fail()
		}
	}
	legacyGone, err := runtime.RetireLegacyCandidatePredecessor(ctx, gate)
	if err != nil {
		return fail()
	}
	allGone := legacyGone
	for i, generation := range generations {
		if generation.RequestID == requestID {
			continue
		}
		c := configs[i]
		gone, err := runtime.CandidateRetired(ctx, c, c.EnvironmentRevision, generation.RequestID, ingress.CandidateRegistrySecretName(c))
		if err != nil {
			return fail()
		}
		allGone = allGone && gone
	}
	after, _, err := runtime.CaptureActivationGate(ctx, app)
	if err != nil || after != gate || !allGone {
		return fail()
	}
	return nil
}
