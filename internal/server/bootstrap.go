package server

import (
	"context"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
)

type AdminTokenCreator interface {
	Create(ctx context.Context, token domain.AdminToken) error
}

func BootstrapAdminToken(ctx context.Context, adminTokens AdminTokenCreator, hashKey, name string) (string, error) {
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
	if err := adminTokens.Create(ctx, domain.AdminToken{
		TokenHash: tokenHash,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return "", err
	}
	return rawToken, nil
}
