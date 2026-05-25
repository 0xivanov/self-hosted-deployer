package db

import (
	"context"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type SecretRepository struct {
	db *Db
}

func NewSecretRepository(db *Db) *SecretRepository {
	return &SecretRepository{db: db}
}

func (r *SecretRepository) Set(ctx context.Context, secret domain.Secret) error {
	_, err := r.db.conn.ExecContext(ctx, `INSERT INTO app_secrets (app_id, name, ciphertext, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(app_id, name) DO UPDATE SET ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
		secret.AppID, secret.Name, secret.Ciphertext, formatTime(secret.CreatedAt), formatTime(secret.UpdatedAt))
	return err
}

func (r *SecretRepository) Find(ctx context.Context, appID string, name string) (domain.Secret, error) {
	var secret domain.Secret
	var createdAt, updatedAt string
	err := r.db.conn.QueryRowContext(ctx, `SELECT app_id, name, ciphertext, created_at, updated_at FROM app_secrets WHERE app_id = ? AND name = ?`, appID, name).
		Scan(&secret.AppID, &secret.Name, &secret.Ciphertext, &createdAt, &updatedAt)
	if err != nil {
		return domain.Secret{}, mapSQLError(err)
	}
	secret.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.Secret{}, err
	}
	secret.UpdatedAt, err = parseStoredTime("updated_at", updatedAt)
	if err != nil {
		return domain.Secret{}, err
	}
	return secret, nil
}

func (r *SecretRepository) ListNamesByApp(ctx context.Context, appID string) ([]string, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT name FROM app_secrets WHERE app_id = ? ORDER BY name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func (r *SecretRepository) Delete(ctx context.Context, appID string, name string) error {
	return mapRowsAffected(r.db.conn.ExecContext(ctx, `DELETE FROM app_secrets WHERE app_id = ? AND name = ?`, appID, name))
}
