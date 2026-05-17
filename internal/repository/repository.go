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
	AdminTokenRepository
	AgentTokenRepository
	JoinTokenRepository
}

type AdminTokenRepository interface {
	CreateAdminToken(ctx context.Context, token AdminToken) error
	FindAdminTokenByHash(ctx context.Context, tokenHash string) (AdminToken, error)
	MarkAdminTokenUsed(ctx context.Context, tokenHash string, usedAt time.Time) error
}

type AgentTokenRepository interface {
	CreateAgentToken(ctx context.Context, token AgentToken) error
	FindAgentTokenByHash(ctx context.Context, tokenHash string) (AgentToken, error)
	MarkAgentTokenUsed(ctx context.Context, tokenHash string, usedAt time.Time) error
}

type JoinTokenRepository interface {
	CreateJoinToken(ctx context.Context, token JoinToken) error
	FindJoinTokenByHash(ctx context.Context, tokenHash string) (JoinToken, error)
}

type NodeRepository interface {
	CreateNode(ctx context.Context, node Node) error
	FindNodeByID(ctx context.Context, nodeID string) (Node, error)
}

type AppRepository interface {
	CreateApp(ctx context.Context, app App) error
	FindAppByName(ctx context.Context, name string) (App, error)
}

type DeploymentRepository interface {
	CreateDeployment(ctx context.Context, deployment Deployment) error
	FindDeploymentByID(ctx context.Context, deploymentID string) (Deployment, error)
}

type SecretRepository interface {
	SetSecret(ctx context.Context, secret Secret) error
	FindSecret(ctx context.Context, appID string, name string) (Secret, error)
}

type RouteRepository interface {
	CreateRoute(ctx context.Context, route Route) error
	FindRouteByDomain(ctx context.Context, domain string) (Route, error)
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

type Node struct {
	ID         string
	Name       string
	Status     string
	LabelsJSON string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type App struct {
	ID               string
	Name             string
	Image            string
	DesiredStateJSON string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

type Deployment struct {
	ID            string
	AppID         string
	Status        string
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Secret struct {
	AppID      string
	Name       string
	Ciphertext string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Route struct {
	ID         string
	AppID      string
	Domain     string
	TargetPort int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
