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
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO app_secrets (app_id, name, ciphertext, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(app_id, name) DO UPDATE SET ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
		secret.AppID, secret.Name, secret.Ciphertext, formatTime(secret.CreatedAt), formatTime(secret.UpdatedAt))
	return err
}

func (r *SecretRepository) Find(ctx context.Context, appID string, name string) (domain.Secret, error) {
	var secret domain.Secret
	var createdAt, updatedAt string
	err := r.db.db.QueryRowContext(ctx, `SELECT app_id, name, ciphertext, created_at, updated_at FROM app_secrets WHERE app_id = ? AND name = ?`, appID, name).
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
