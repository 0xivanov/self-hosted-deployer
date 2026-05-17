package server

import (
	"context"

	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/repository"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

type PlatformService struct {
	deployerv1.UnimplementedPlatformServiceServer
	repo repository.HealthChecker
}

func NewPlatformService(repo repository.HealthChecker) PlatformService {
	return PlatformService{repo: repo}
}

func (s PlatformService) GetVersion(context.Context, *deployerv1.GetVersionRequest) (*deployerv1.GetVersionResponse, error) {
	current := version.Current()
	return &deployerv1.GetVersionResponse{
		Version:   current.Version,
		Commit:    current.Commit,
		BuildDate: current.BuildDate,
	}, nil
}

func (s PlatformService) GetStatus(ctx context.Context, _ *deployerv1.GetStatusRequest) (*deployerv1.GetStatusResponse, error) {
	current := version.Current()
	return &deployerv1.GetStatusResponse{
		Version:   current.Version,
		Commit:    current.Commit,
		BuildDate: current.BuildDate,
		Ready:     s.repo.Ping(ctx) == nil,
	}, nil
}
