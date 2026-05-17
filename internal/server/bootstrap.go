package server

import (
	"context"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/repository"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
)

func BootstrapAdminToken(ctx context.Context, repo repository.Repository, hashKey, name string) (string, error) {
	rawToken, err := security.NewToken(security.AdminTokenPrefix)
	if err != nil {
		return "", err
	}
	tokenHash, err := security.HashToken([]byte(hashKey), rawToken)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = "bootstrap"
	}
	if err := repo.CreateAdminToken(ctx, repository.AdminToken{
		TokenHash: tokenHash,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return "", err
	}
	return rawToken, nil
}
