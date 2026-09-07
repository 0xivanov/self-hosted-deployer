package server

import (
	"context"
	"errors"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PreflightApp validates a proposed desired state and the current environment
// without reserving capacity or writing application, deployment, route, event,
// or secret state. It intentionally shares the same validation gates as
// DeployApp before that method's first mutation.
func (s AppService) PreflightApp(ctx context.Context, req *deployerv1.PreflightAppRequest) (*deployerv1.PreflightAppResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetDeployerYaml()) == "" {
		return nil, status.Error(codes.InvalidArgument, "deployer_yaml is required")
	}
	cfg, err := appconfig.Parse([]byte(req.GetDeployerYaml()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := preflightHosting(ctx, s.runtime, cfg); err != nil {
		return nil, err
	}
	if s.apps != nil {
		existing, findErr := s.apps.FindByName(ctx, cfg.Name)
		if findErr != nil && !errors.Is(findErr, db.ErrNotFound) {
			return nil, status.Error(codes.Internal, "find app")
		}
		if findErr == nil {
			if err := rejectHostingProfileRemoval(cfg, existing); err != nil {
				return nil, err
			}
		}
	}
	if err := s.validateRequestedDomain(ctx, cfg.Name, cfg.Routing.Domain); err != nil {
		return nil, err
	}
	desiredState, err := cfg.JSON()
	if err != nil {
		return nil, status.Error(codes.Internal, "encode desired state")
	}
	warnings := []string{
		"preflight is a read-only snapshot and does not reserve capacity",
		"backup and restore evidence, alert delivery, external availability monitoring, off-host audit retention, fleet pins, and CNI enforcement require separate verification",
	}
	if cfg.Hosting == nil {
		warnings = append(warnings, "hosting profile is not declared; legacy rendering would be used")
	}
	return &deployerv1.PreflightAppResponse{DesiredState: desiredState, Warnings: warnings}, nil
}
