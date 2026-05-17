package memory

import (
	"context"
	"sync"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/repository"
)

type Repository struct {
	mu          sync.Mutex
	adminTokens map[string]repository.AdminToken
	agentTokens map[string]repository.AgentToken
	joinTokens  map[string]repository.JoinToken
}

func New() *Repository {
	return &Repository{
		adminTokens: make(map[string]repository.AdminToken),
		agentTokens: make(map[string]repository.AgentToken),
		joinTokens:  make(map[string]repository.JoinToken),
	}
}

func (r *Repository) Ping(context.Context) error {
	return nil
}

func (r *Repository) CreateAdminToken(_ context.Context, token repository.AdminToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adminTokens[token.TokenHash] = token
	return nil
}

func (r *Repository) FindAdminTokenByHash(_ context.Context, tokenHash string) (repository.AdminToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.adminTokens[tokenHash]
	if !ok {
		return repository.AdminToken{}, repository.ErrNotFound
	}
	return token, nil
}

func (r *Repository) MarkAdminTokenUsed(_ context.Context, tokenHash string, usedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.adminTokens[tokenHash]
	if !ok {
		return repository.ErrNotFound
	}
	token.LastUsedAt = &usedAt
	r.adminTokens[tokenHash] = token
	return nil
}

func (r *Repository) CreateAgentToken(_ context.Context, token repository.AgentToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentTokens[token.TokenHash] = token
	return nil
}

func (r *Repository) FindAgentTokenByHash(_ context.Context, tokenHash string) (repository.AgentToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.agentTokens[tokenHash]
	if !ok {
		return repository.AgentToken{}, repository.ErrNotFound
	}
	return token, nil
}

func (r *Repository) MarkAgentTokenUsed(_ context.Context, tokenHash string, usedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.agentTokens[tokenHash]
	if !ok {
		return repository.ErrNotFound
	}
	token.LastUsedAt = &usedAt
	r.agentTokens[tokenHash] = token
	return nil
}

func (r *Repository) CreateJoinToken(_ context.Context, token repository.JoinToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.joinTokens[token.TokenHash] = token
	return nil
}

func (r *Repository) FindJoinTokenByHash(_ context.Context, tokenHash string) (repository.JoinToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.joinTokens[tokenHash]
	if !ok {
		return repository.JoinToken{}, repository.ErrNotFound
	}
	return token, nil
}
