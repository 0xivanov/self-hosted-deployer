package repository

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

type HealthChecker interface {
	Ping(ctx context.Context) error
}

type Repository interface {
	HealthChecker
	CreateAdminToken(ctx context.Context, token AdminToken) error
	FindAdminTokenByHash(ctx context.Context, tokenHash string) (AdminToken, error)
	MarkAdminTokenUsed(ctx context.Context, tokenHash string, usedAt time.Time) error
	CreateAgentToken(ctx context.Context, token AgentToken) error
	FindAgentTokenByHash(ctx context.Context, tokenHash string) (AgentToken, error)
	MarkAgentTokenUsed(ctx context.Context, tokenHash string, usedAt time.Time) error
	CreateJoinToken(ctx context.Context, token JoinToken) error
	FindJoinTokenByHash(ctx context.Context, tokenHash string) (JoinToken, error)
}

type AdminToken struct {
	TokenHash  string
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type AgentToken struct {
	TokenHash  string
	NodeID     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type JoinToken struct {
	TokenHash        string
	IntendedNodeName string
	LabelsJSON       string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	UsedAt           *time.Time
}
