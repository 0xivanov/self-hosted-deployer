package db

import (
	"context"
	"database/sql"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type JoinTokenRepository struct {
	db *Db
}

func NewJoinTokenRepository(db *Db) *JoinTokenRepository {
	return &JoinTokenRepository{db: db}
}

func (r *JoinTokenRepository) Create(ctx context.Context, token domain.JoinToken) error {
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO node_join_tokens (token_hash, intended_node_name, labels_json, created_at, expires_at, used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		token.TokenHash, token.IntendedNodeName, token.LabelsJSON, formatTime(token.CreatedAt), formatTime(token.ExpiresAt), formatOptionalTime(token.UsedAt))
	return err
}

func (r *JoinTokenRepository) FindByHash(ctx context.Context, tokenHash string) (domain.JoinToken, error) {
	var token domain.JoinToken
	var createdAt, expiresAt string
	var usedAt sql.NullString
	err := r.db.db.QueryRowContext(ctx, `SELECT token_hash, intended_node_name, labels_json, created_at, expires_at, used_at FROM node_join_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.IntendedNodeName, &token.LabelsJSON, &createdAt, &expiresAt, &usedAt)
	if err != nil {
		return domain.JoinToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.ExpiresAt = parseStoredTime(expiresAt)
	token.UsedAt = parseOptionalStoredTime(usedAt)
	return token, nil
}
