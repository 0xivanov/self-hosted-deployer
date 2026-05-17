package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type SQLiteRepository struct {
	db *sql.DB
}

func OpenSQLite(ctx context.Context, dsn string) (*SQLiteRepository, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	repo := &SQLiteRepository{db: db}
	if err := repo.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repo, nil
}

func (r *SQLiteRepository) Close() error {
	return r.db.Close()
}

func (r *SQLiteRepository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

func (r *SQLiteRepository) Migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS admin_tokens (
			token_hash TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			revoked_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL,
			labels_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_join_tokens (
			token_hash TEXT PRIMARY KEY,
			intended_node_name TEXT,
			labels_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			used_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS agent_tokens (
			token_hash TEXT PRIMARY KEY,
			node_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			revoked_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS apps (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			image TEXT NOT NULL,
			desired_state_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS deployments (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			status TEXT NOT NULL,
			failure_reason TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS app_secrets (
			app_id TEXT NOT NULL,
			name TEXT NOT NULL,
			ciphertext TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (app_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS routes (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			domain TEXT NOT NULL UNIQUE,
			target_port INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}

	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate sqlite: %w", err)
		}
	}
	return nil
}

func (r *SQLiteRepository) CreateAdminToken(ctx context.Context, token AdminToken) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO admin_tokens (token_hash, display_name, created_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?)`,
		token.TokenHash, token.Name, formatTime(token.CreatedAt), formatOptionalTime(token.LastUsedAt), formatOptionalTime(token.RevokedAt))
	return err
}

func (r *SQLiteRepository) FindAdminTokenByHash(ctx context.Context, tokenHash string) (AdminToken, error) {
	var token AdminToken
	var createdAt string
	var lastUsedAt, revokedAt sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT token_hash, display_name, created_at, last_used_at, revoked_at FROM admin_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.Name, &createdAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return AdminToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.LastUsedAt = parseOptionalStoredTime(lastUsedAt)
	token.RevokedAt = parseOptionalStoredTime(revokedAt)
	return token, nil
}

func (r *SQLiteRepository) MarkAdminTokenUsed(ctx context.Context, tokenHash string, usedAt time.Time) error {
	return mapRowsAffected(r.db.ExecContext(ctx, `UPDATE admin_tokens SET last_used_at = ? WHERE token_hash = ?`, formatTime(usedAt), tokenHash))
}

func (r *SQLiteRepository) CreateAgentToken(ctx context.Context, token AgentToken) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO agent_tokens (token_hash, node_id, created_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?)`,
		token.TokenHash, token.NodeID, formatTime(token.CreatedAt), formatOptionalTime(token.LastUsedAt), formatOptionalTime(token.RevokedAt))
	return err
}

func (r *SQLiteRepository) FindAgentTokenByHash(ctx context.Context, tokenHash string) (AgentToken, error) {
	var token AgentToken
	var createdAt string
	var lastUsedAt, revokedAt sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT token_hash, node_id, created_at, last_used_at, revoked_at FROM agent_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.NodeID, &createdAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return AgentToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.LastUsedAt = parseOptionalStoredTime(lastUsedAt)
	token.RevokedAt = parseOptionalStoredTime(revokedAt)
	return token, nil
}

func (r *SQLiteRepository) MarkAgentTokenUsed(ctx context.Context, tokenHash string, usedAt time.Time) error {
	return mapRowsAffected(r.db.ExecContext(ctx, `UPDATE agent_tokens SET last_used_at = ? WHERE token_hash = ?`, formatTime(usedAt), tokenHash))
}

func (r *SQLiteRepository) CreateJoinToken(ctx context.Context, token JoinToken) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO node_join_tokens (token_hash, intended_node_name, labels_json, created_at, expires_at, used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		token.TokenHash, token.IntendedNodeName, token.LabelsJSON, formatTime(token.CreatedAt), formatTime(token.ExpiresAt), formatOptionalTime(token.UsedAt))
	return err
}

func (r *SQLiteRepository) FindJoinTokenByHash(ctx context.Context, tokenHash string) (JoinToken, error) {
	var token JoinToken
	var createdAt, expiresAt string
	var usedAt sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT token_hash, intended_node_name, labels_json, created_at, expires_at, used_at FROM node_join_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&token.TokenHash, &token.IntendedNodeName, &token.LabelsJSON, &createdAt, &expiresAt, &usedAt)
	if err != nil {
		return JoinToken{}, mapSQLError(err)
	}
	token.CreatedAt = parseStoredTime(createdAt)
	token.ExpiresAt = parseStoredTime(expiresAt)
	token.UsedAt = parseOptionalStoredTime(usedAt)
	return token, nil
}

func mapSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
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
		return ErrNotFound
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
