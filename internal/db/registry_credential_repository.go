package db

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

var (
	ErrRegistryCredentialLimit   = errors.New("registry credential revision limit reached")
	ErrRegistryCredentialExists  = errors.New("registry credential revision already exists")
	ErrInvalidRegistryCredential = errors.New("invalid registry credential")
)

const maxRegistryCredentialRevisions = 100

var (
	registryCredentialRevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	registryCredentialAppPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

type RegistryCredentialRepository struct {
	db *Db
}

func NewRegistryCredentialRepository(db *Db) *RegistryCredentialRepository {
	return &RegistryCredentialRepository{db: db}
}

func (r *RegistryCredentialRepository) Create(ctx context.Context, credential domain.RegistryCredential) error {
	if !validRegistryCredential(credential) {
		return ErrInvalidRegistryCredential
	}
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM registry_credentials WHERE app_name = ? AND revision = ?`, credential.AppName, credential.Revision).Scan(&exists)
	if err == nil {
		return ErrRegistryCredentialExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO registry_credentials (app_name, revision, registry, ciphertext, created_at)
		SELECT ?, ?, ?, ?, ?
		WHERE (SELECT count(*) FROM registry_credentials WHERE app_name = ?) < ?`,
		credential.AppName, credential.Revision, credential.Registry, credential.Ciphertext, formatTime(credential.CreatedAt),
		credential.AppName, maxRegistryCredentialRevisions)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrRegistryCredentialLimit
	}
	return tx.Commit()
}

func (r *RegistryCredentialRepository) Find(ctx context.Context, appName string, revision string) (domain.RegistryCredential, error) {
	if !registryCredentialAppPattern.MatchString(appName) || !registryCredentialRevisionPattern.MatchString(revision) {
		return domain.RegistryCredential{}, ErrInvalidRegistryCredential
	}
	var credential domain.RegistryCredential
	var createdAt string
	err := r.db.conn.QueryRowContext(ctx, `SELECT app_name, revision, registry, ciphertext, created_at
		FROM registry_credentials WHERE app_name = ? AND revision = ?`, appName, revision).
		Scan(&credential.AppName, &credential.Revision, &credential.Registry, &credential.Ciphertext, &createdAt)
	if err != nil {
		return domain.RegistryCredential{}, mapSQLError(err)
	}
	credential.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.RegistryCredential{}, err
	}
	return credential, nil
}

func (r *RegistryCredentialRepository) ListByApp(ctx context.Context, appName string) ([]domain.RegistryCredential, error) {
	if !registryCredentialAppPattern.MatchString(appName) {
		return nil, ErrInvalidRegistryCredential
	}
	rows, err := r.db.conn.QueryContext(ctx, `SELECT app_name, revision, registry, created_at
		FROM registry_credentials WHERE app_name = ? ORDER BY revision`, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credentials := []domain.RegistryCredential{}
	for rows.Next() {
		var credential domain.RegistryCredential
		var createdAt string
		if err = rows.Scan(&credential.AppName, &credential.Revision, &credential.Registry, &createdAt); err != nil {
			return nil, err
		}
		credential.CreatedAt, err = parseStoredTime("created_at", createdAt)
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return credentials, nil
}

func (r *RegistryCredentialRepository) DeleteByApp(ctx context.Context, appName string) error {
	if !registryCredentialAppPattern.MatchString(appName) {
		return ErrInvalidRegistryCredential
	}
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM registry_credentials WHERE app_name = ?`, appName)
	return err
}

func validRegistryCredential(credential domain.RegistryCredential) bool {
	if !registryCredentialRevisionPattern.MatchString(credential.Revision) {
		return false
	}
	if !registryCredentialAppPattern.MatchString(credential.AppName) {
		return false
	}
	if credential.Registry != "docker.io" && credential.Registry != "ghcr.io" {
		return false
	}
	return credential.Ciphertext != "" && len(credential.Ciphertext) <= 32768
}
