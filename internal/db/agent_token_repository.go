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
	_, err := r.db.conn.ExecContext(ctx, `INSERT INTO agent_tokens (token_hash, node_id, created_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?)`,
		token.TokenHash, token.NodeID, formatTime(token.CreatedAt), formatOptionalTime(token.LastUsedAt), formatOptionalTime(token.RevokedAt))
	return err
}

func (r *AgentTokenRepository) FindByHash(ctx context.Context, tokenHash string) (domain.AgentToken, error) {
	var token domain.AgentToken
	var createdAt string
	var lastUsedAt, revokedAt sql.NullString
	err := r.db.conn.QueryRowContext(ctx, `SELECT token_hash, node_id, created_at, last_used_at, revoked_at FROM agent_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.NodeID, &createdAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return domain.AgentToken{}, mapSQLError(err)
	}
	token.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.AgentToken{}, err
	}
	token.LastUsedAt, err = parseOptionalStoredTime("last_used_at", lastUsedAt)
	if err != nil {
		return domain.AgentToken{}, err
	}
	token.RevokedAt, err = parseOptionalStoredTime("revoked_at", revokedAt)
	if err != nil {
		return domain.AgentToken{}, err
	}
	return token, nil
}

func (r *AgentTokenRepository) MarkUsed(ctx context.Context, tokenHash string, usedAt time.Time) error {
	return mapRowsAffected(r.db.conn.ExecContext(ctx, `UPDATE agent_tokens SET last_used_at = ? WHERE token_hash = ?`, formatTime(usedAt), tokenHash))
}

func (r *AgentTokenRepository) RevokeByNodeID(ctx context.Context, nodeID string, revokedAt time.Time) error {
	_, err := r.db.conn.ExecContext(ctx, `UPDATE agent_tokens SET revoked_at = ? WHERE node_id = ? AND revoked_at IS NULL`, formatTime(revokedAt), nodeID)
	return err
}

func (r *AgentTokenRepository) DeleteByNodeID(ctx context.Context, nodeID string) error {
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM agent_tokens WHERE node_id = ?`, nodeID)
	return err
}
