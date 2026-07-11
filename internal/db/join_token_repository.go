package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

var (
	ErrJoinTokenExpired = errors.New("join token expired")
	ErrJoinTokenUsed    = errors.New("join token already used")
)

type JoinTokenRepository struct {
	db *Db
}

func NewJoinTokenRepository(db *Db) *JoinTokenRepository {
	return &JoinTokenRepository{db: db}
}

func (r *JoinTokenRepository) Create(ctx context.Context, token domain.JoinToken) error {
	_, err := r.db.conn.ExecContext(ctx, `INSERT INTO node_join_tokens (token_hash, intended_node_name, labels_json, created_at, expires_at, used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		token.TokenHash, token.IntendedNodeName, token.LabelsJSON, formatTime(token.CreatedAt), formatTime(token.ExpiresAt), formatOptionalTime(token.UsedAt))
	return err
}

func (r *JoinTokenRepository) FindByHash(ctx context.Context, tokenHash string) (domain.JoinToken, error) {
	var token domain.JoinToken
	var createdAt, expiresAt string
	var usedAt sql.NullString
	err := r.db.conn.QueryRowContext(ctx, `SELECT token_hash, intended_node_name, labels_json, created_at, expires_at, used_at FROM node_join_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.IntendedNodeName, &token.LabelsJSON, &createdAt, &expiresAt, &usedAt)
	if err != nil {
		return domain.JoinToken{}, mapSQLError(err)
	}
	token.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.JoinToken{}, err
	}
	token.ExpiresAt, err = parseStoredTime("expires_at", expiresAt)
	if err != nil {
		return domain.JoinToken{}, err
	}
	token.UsedAt, err = parseOptionalStoredTime("used_at", usedAt)
	if err != nil {
		return domain.JoinToken{}, err
	}
	return token, nil
}

func (r *JoinTokenRepository) Consume(ctx context.Context, tokenHash string, usedAt time.Time) (domain.JoinToken, error) {
	token, err := r.FindByHash(ctx, tokenHash)
	if err != nil {
		return domain.JoinToken{}, err
	}
	if token.UsedAt != nil {
		return domain.JoinToken{}, ErrJoinTokenUsed
	}
	if !token.ExpiresAt.After(usedAt.UTC()) {
		return domain.JoinToken{}, ErrJoinTokenExpired
	}
	if err := mapRowsAffected(r.db.conn.ExecContext(ctx, `UPDATE node_join_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL`, formatTime(usedAt), tokenHash)); err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.JoinToken{}, ErrJoinTokenUsed
		}
		return domain.JoinToken{}, err
	}
	token.UsedAt = &usedAt
	return token, nil
}

func (r *JoinTokenRepository) DeleteByNodeName(ctx context.Context, nodeName string) error {
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM node_join_tokens WHERE intended_node_name = ?`, nodeName)
	return err
}
