package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type AgentTokenRepository struct {
	db *Db
}

func NewAgentTokenRepository(db *Db) *AgentTokenRepository {
	return &AgentTokenRepository{db: db}
}

func (r *AgentTokenRepository) Create(ctx context.Context, token domain.AgentToken) error {
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO agent_tokens (token_hash, node_id, created_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?)`,
		token.TokenHash, token.NodeID, formatTime(token.CreatedAt), formatOptionalTime(token.LastUsedAt), formatOptionalTime(token.RevokedAt))
	return err
}

func (r *AgentTokenRepository) FindByHash(ctx context.Context, tokenHash string) (domain.AgentToken, error) {
	var token domain.AgentToken
	var createdAt string
	var lastUsedAt, revokedAt sql.NullString
	err := r.db.db.QueryRowContext(ctx, `SELECT token_hash, node_id, created_at, last_used_at, revoked_at FROM agent_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.NodeID, &createdAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return domain.AgentToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.LastUsedAt = parseOptionalStoredTime(lastUsedAt)
	token.RevokedAt = parseOptionalStoredTime(revokedAt)
	return token, nil
}

func (r *AgentTokenRepository) MarkUsed(ctx context.Context, tokenHash string, usedAt time.Time) error {
	return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE agent_tokens SET last_used_at = ? WHERE token_hash = ?`, formatTime(usedAt), tokenHash))
}
