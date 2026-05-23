package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type AdminTokenRepository struct {
	db *Db
}

func NewAdminTokenRepository(db *Db) *AdminTokenRepository {
	return &AdminTokenRepository{db: db}
}

func (r *AdminTokenRepository) Create(ctx context.Context, token domain.AdminToken) error {
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO admin_tokens (token_hash, display_name, created_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?)`,
		token.TokenHash, token.Name, formatTime(token.CreatedAt), formatOptionalTime(token.LastUsedAt), formatOptionalTime(token.RevokedAt))
	return err
}

func (r *AdminTokenRepository) FindByHash(ctx context.Context, tokenHash string) (domain.AdminToken, error) {
	var token domain.AdminToken
	var createdAt string
	var lastUsedAt, revokedAt sql.NullString
	err := r.db.db.QueryRowContext(ctx, `SELECT token_hash, display_name, created_at, last_used_at, revoked_at FROM admin_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.Name, &createdAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return domain.AdminToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.LastUsedAt = parseOptionalStoredTime(lastUsedAt)
	token.RevokedAt = parseOptionalStoredTime(revokedAt)
	return token, nil
}

func (r *AdminTokenRepository) MarkUsed(ctx context.Context, tokenHash string, usedAt time.Time) error {
	return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE admin_tokens SET last_used_at = ? WHERE token_hash = ?`, formatTime(usedAt), tokenHash))
}
