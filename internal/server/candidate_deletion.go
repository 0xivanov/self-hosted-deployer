package server

import (
	"context"
	"errors"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type candidateGenerationReader interface {
	GenerationsByApp(context.Context, string, string) ([]domain.CandidateGeneration, error)
}

type candidateDeletionGuard interface {
	CheckCandidateDeletion(context.Context, string) error
}

type candidateDeletionRuntime interface {
	candidateDeletionGuard
	BeginCandidateDeletion(context.Context, appconfig.Config, string, bool) (ingress.ActivationGate, error)
	RetireCandidateDeployment(context.Context, appconfig.Config, string, string, string) error
	CandidateRetired(context.Context, appconfig.Config, string, string, string) (bool, error)
	CleanupCandidateDeletion(context.Context, ingress.ActivationGate, string) (bool, error)
	FinishCandidateDeletion(context.Context, ingress.ActivationGate, string) error
}

var _ candidateDeletionRuntime = (*ingress.Controller)(nil)

// The Service deletion marker is the durable admission barrier. It is retained
// through database cleanup and app deletion, and only removed by the final CAS.
// As with candidate activation, exactly one server writer must own the app lock.
func (s AppService) checkCandidateDeletion(ctx context.Context, app string) error {
	if guard, ok := s.runtime.(candidateDeletionGuard); ok {
		if err := guard.CheckCandidateDeletion(ctx, app); err != nil {
			return status.Error(codes.FailedPrecondition, "app deletion is pending or its status cannot be confirmed")
		}
	}
	return nil
}

// tryDeleteCandidateApp runs under DeleteApp's mutation lock, after checking
// pending requests. A hidden initial app must also be cleaned up on removal.
func (s AppService) tryDeleteCandidateApp(ctx context.Context, name string) (*deployerv1.DeleteAppResponse, bool, error) {
	history, ok := s.candidateBindings.(candidateGenerationReader)
	if !ok {
		return nil, false, nil
	}
	app, err := s.apps.FindByName(ctx, name)
	if errors.Is(err, db.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, status.Error(codes.Internal, "read app for deletion")
	}
	generations, err := history.GenerationsByApp(ctx, name, app.ID)
	if err != nil {
		return nil, true, status.Error(codes.Internal, "read candidate generations")
	}
	if len(generations) == 0 {
		return nil, false, nil
	}
	runtime, ok := s.runtime.(candidateDeletionRuntime)
	if !s.enableCandidateOperations || !ok {
		return nil, true, status.Error(codes.FailedPrecondition, "candidate deletion is not enabled or configured")
	}
	cfg, err := appconfig.FromJSON(app.DesiredStateJSON)
	if err != nil || cfg.Validate() != nil || cfg.Name != name || cfg.State.Mode != appconfig.DefaultStateMode {
		return nil, true, status.Error(codes.FailedPrecondition, "candidate app configuration is invalid")
	}
	configs := make([]appconfig.Config, len(generations))
	for i, generation := range generations {
		if generation.AppName != name || generation.AppID != app.ID || !registryauth.ValidRevision(generation.RequestID) || (generation.State != "applied" && generation.State != "withdrawn") {
			return nil, true, status.Error(codes.FailedPrecondition, "candidate history is incomplete")
		}
		configs[i], err = appconfig.FromJSON(generation.RequestedState)
		if err != nil || configs[i].Name != name || configs[i].Validate() != nil || configs[i].Hosting == nil || configs[i].State.Mode != appconfig.DefaultStateMode || ingress.ValidateCandidateReferences(configs[i], configs[i].EnvironmentRevision, ingress.CandidateRegistrySecretName(configs[i])) != nil {
			return nil, true, status.Error(codes.FailedPrecondition, "candidate generation configuration is invalid")
		}
	}
	gate, err := runtime.BeginCandidateDeletion(ctx, cfg, app.ID, app.DeletedAt != nil)
	if err != nil {
		return nil, true, status.Error(codes.FailedPrecondition, "candidate deletion fence is unconfirmed; retry deletion")
	}
	// Retire every generation before waiting for any one of them, so cleanup
	// does not need one worker pass per previously deployed release.
	for i, generation := range generations {
		c := configs[i]
		if err = runtime.RetireCandidateDeployment(ctx, c, c.EnvironmentRevision, generation.RequestID, ingress.CandidateRegistrySecretName(c)); err != nil {
			return nil, true, status.Error(codes.FailedPrecondition, "candidate retirement remains pending; retry deletion")
		}
	}
	for i, generation := range generations {
		c := configs[i]
		retired, retirementErr := runtime.CandidateRetired(ctx, c, c.EnvironmentRevision, generation.RequestID, ingress.CandidateRegistrySecretName(c))
		if retirementErr != nil || !retired {
			return nil, true, status.Error(codes.FailedPrecondition, "candidate workloads are draining; retry deletion")
		}
	}
	clean, err := runtime.CleanupCandidateDeletion(ctx, gate, app.ID)
	if err != nil || !clean {
		return nil, true, status.Error(codes.FailedPrecondition, "candidate resources are being removed; retry deletion")
	}
	if s.routes != nil {
		if err = s.routes.DeleteByApp(ctx, app.ID); err != nil {
			return nil, true, status.Error(codes.Internal, "delete candidate routes")
		}
	}
	if s.registryCredentialRepository != nil {
		if err = s.registryCredentialRepository.DeleteByApp(ctx, name); err != nil {
			return nil, true, status.Error(codes.Internal, "delete candidate registry credentials")
		}
	}
	if s.environmentBundles != nil {
		if err = s.environmentBundles.DeleteByApp(ctx, name); err != nil {
			return nil, true, status.Error(codes.Internal, "delete candidate environment settings")
		}
	}
	if app.DeletedAt == nil {
		app, err = s.apps.MarkDeleted(ctx, name, s.now().UTC())
		if err != nil {
			return nil, true, status.Error(codes.Internal, "mark candidate app deleted")
		}
	}
	if err = runtime.FinishCandidateDeletion(ctx, gate, app.ID); err != nil {
		return nil, true, status.Error(codes.FailedPrecondition, "final candidate cleanup remains pending; retry deletion")
	}
	result, err := protoApp(app)
	if err != nil {
		return nil, true, status.Error(codes.Internal, "decode deleted candidate app")
	}
	return &deployerv1.DeleteAppResponse{App: result}, true, nil
}
