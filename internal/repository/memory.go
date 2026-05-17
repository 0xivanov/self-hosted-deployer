package repository

import (
	"context"
	"sync"
	"time"
)

type MemoryRepository struct {
	mu          sync.Mutex
	adminTokens map[string]AdminToken
	agentTokens map[string]AgentToken
	joinTokens  map[string]JoinToken
}

func NewMemory() *MemoryRepository {
	return &MemoryRepository{
		adminTokens: make(map[string]AdminToken),
		agentTokens: make(map[string]AgentToken),
		joinTokens:  make(map[string]JoinToken),
	}
}

func (r *MemoryRepository) Ping(context.Context) error {
	return nil
}

func (r *MemoryRepository) CreateAdminToken(_ context.Context, token AdminToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adminTokens[token.TokenHash] = token
	return nil
}

func (r *MemoryRepository) FindAdminTokenByHash(_ context.Context, tokenHash string) (AdminToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.adminTokens[tokenHash]
	if !ok {
		return AdminToken{}, ErrNotFound
	}
	return token, nil
}

func (r *MemoryRepository) MarkAdminTokenUsed(_ context.Context, tokenHash string, usedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.adminTokens[tokenHash]
	if !ok {
		return ErrNotFound
	}
	token.LastUsedAt = &usedAt
	r.adminTokens[tokenHash] = token
	return nil
}

func (r *MemoryRepository) CreateAgentToken(_ context.Context, token AgentToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentTokens[token.TokenHash] = token
	return nil
}

func (r *MemoryRepository) FindAgentTokenByHash(_ context.Context, tokenHash string) (AgentToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.agentTokens[tokenHash]
	if !ok {
		return AgentToken{}, ErrNotFound
	}
	return token, nil
}

func (r *MemoryRepository) MarkAgentTokenUsed(_ context.Context, tokenHash string, usedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.agentTokens[tokenHash]
	if !ok {
		return ErrNotFound
	}
	token.LastUsedAt = &usedAt
	r.agentTokens[tokenHash] = token
	return nil
}

func (r *MemoryRepository) CreateJoinToken(_ context.Context, token JoinToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.joinTokens[token.TokenHash] = token
	return nil
}

func (r *MemoryRepository) FindJoinTokenByHash(_ context.Context, tokenHash string) (JoinToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.joinTokens[tokenHash]
	if !ok {
		return JoinToken{}, ErrNotFound
	}
	return token, nil
}
