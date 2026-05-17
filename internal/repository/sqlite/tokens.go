package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/repository"
)

func (r *Repository) CreateAdminToken(ctx context.Context, token repository.AdminToken) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO admin_tokens (token_hash, display_name, created_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?)`,
		token.TokenHash, token.Name, formatTime(token.CreatedAt), formatOptionalTime(token.LastUsedAt), formatOptionalTime(token.RevokedAt))
	return err
}

func (r *Repository) FindAdminTokenByHash(ctx context.Context, tokenHash string) (repository.AdminToken, error) {
	var token repository.AdminToken
	var createdAt string
	var lastUsedAt, revokedAt sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT token_hash, display_name, created_at, last_used_at, revoked_at FROM admin_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.Name, &createdAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return repository.AdminToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.LastUsedAt = parseOptionalStoredTime(lastUsedAt)
	token.RevokedAt = parseOptionalStoredTime(revokedAt)
	return token, nil
}

func (r *Repository) MarkAdminTokenUsed(ctx context.Context, tokenHash string, usedAt time.Time) error {
	return mapRowsAffected(r.db.ExecContext(ctx, `UPDATE admin_tokens SET last_used_at = ? WHERE token_hash = ?`, formatTime(usedAt), tokenHash))
}

func (r *Repository) CreateAgentToken(ctx context.Context, token repository.AgentToken) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO agent_tokens (token_hash, node_id, created_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?)`,
		token.TokenHash, token.NodeID, formatTime(token.CreatedAt), formatOptionalTime(token.LastUsedAt), formatOptionalTime(token.RevokedAt))
	return err
}

func (r *Repository) FindAgentTokenByHash(ctx context.Context, tokenHash string) (repository.AgentToken, error) {
	var token repository.AgentToken
	var createdAt string
	var lastUsedAt, revokedAt sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT token_hash, node_id, created_at, last_used_at, revoked_at FROM agent_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.NodeID, &createdAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return repository.AgentToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.LastUsedAt = parseOptionalStoredTime(lastUsedAt)
	token.RevokedAt = parseOptionalStoredTime(revokedAt)
	return token, nil
}

func (r *Repository) MarkAgentTokenUsed(ctx context.Context, tokenHash string, usedAt time.Time) error {
	return mapRowsAffected(r.db.ExecContext(ctx, `UPDATE agent_tokens SET last_used_at = ? WHERE token_hash = ?`, formatTime(usedAt), tokenHash))
}

func (r *Repository) CreateJoinToken(ctx context.Context, token repository.JoinToken) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO node_join_tokens (token_hash, intended_node_name, labels_json, created_at, expires_at, used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		token.TokenHash, token.IntendedNodeName, token.LabelsJSON, formatTime(token.CreatedAt), formatTime(token.ExpiresAt), formatOptionalTime(token.UsedAt))
	return err
}

func (r *Repository) FindJoinTokenByHash(ctx context.Context, tokenHash string) (repository.JoinToken, error) {
	var token repository.JoinToken
	var createdAt, expiresAt string
	var usedAt sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT token_hash, intended_node_name, labels_json, created_at, expires_at, used_at FROM node_join_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.IntendedNodeName, &token.LabelsJSON, &createdAt, &expiresAt, &usedAt)
	if err != nil {
		return repository.JoinToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.ExpiresAt = parseStoredTime(expiresAt)
	token.UsedAt = parseOptionalStoredTime(usedAt)
	return token, nil
}

func mapSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return repository.ErrNotFound
	}
	return err
}

func mapRowsAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*t), Valid: true}
}

func parseStoredTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func parseOptionalStoredTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	t := parseStoredTime(value.String)
	return &t
}
