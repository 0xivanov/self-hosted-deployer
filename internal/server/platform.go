package server

import (
	"context"

	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/version"
)

type HealthRepository interface {
	Ping(ctx context.Context) error
}

type PlatformService struct {
	deployerv1.UnimplementedPlatformServiceServer
	health         HealthRepository
	serverIdentity string
}

func NewPlatformService(health HealthRepository, serverIdentity ...string) PlatformService {
	identity := ""
	if len(serverIdentity) > 0 {
		identity = serverIdentity[0]
	}
	return PlatformService{health: health, serverIdentity: identity}
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
		Version:        current.Version,
		Commit:         current.Commit,
		BuildDate:      current.BuildDate,
		Ready:          s.health.Ping(ctx) == nil,
		ServerIdentity: s.serverIdentity,
	}, nil
}
