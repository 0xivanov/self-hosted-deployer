package db

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

var ErrEnvironmentBundleLimit = errors.New("environment bundle revision limit reached")
var ErrEnvironmentBundleExists = errors.New("environment bundle revision already exists")
var environmentRevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type EnvironmentBundleRepository struct{ db *Db }

func NewEnvironmentBundleRepository(db *Db) *EnvironmentBundleRepository {
	return &EnvironmentBundleRepository{db: db}
}

func (r *EnvironmentBundleRepository) Create(ctx context.Context, bundle domain.EnvironmentBundle) error {
	if !registryCredentialAppPattern.MatchString(bundle.AppName) || bundle.Ciphertext == "" || !environmentRevisionPattern.MatchString(bundle.Revision) {
		return errors.New("invalid environment bundle")
	}
	var exists int
	err := r.db.conn.QueryRowContext(ctx, `SELECT 1 FROM environment_bundles WHERE app_name = ? AND revision = ?`, bundle.AppName, bundle.Revision).Scan(&exists)
	if err == nil {
		return ErrEnvironmentBundleExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	result, err := r.db.conn.ExecContext(ctx, `INSERT INTO environment_bundles (app_name, revision, names_json, ciphertext, created_at) SELECT ?, ?, ?, ?, ? WHERE (SELECT count(*) FROM environment_bundles WHERE app_name = ?) < 100`, bundle.AppName, bundle.Revision, bundle.NamesJSON, bundle.Ciphertext, formatTime(bundle.CreatedAt), bundle.AppName)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrEnvironmentBundleLimit
	}
	return nil
}

func (r *EnvironmentBundleRepository) Find(ctx context.Context, appName, revision string) (domain.EnvironmentBundle, error) {
	var b domain.EnvironmentBundle
	var created string
	err := r.db.conn.QueryRowContext(ctx, `SELECT app_name, revision, names_json, ciphertext, created_at FROM environment_bundles WHERE app_name = ? AND revision = ?`, appName, revision).Scan(&b.AppName, &b.Revision, &b.NamesJSON, &b.Ciphertext, &created)
	if err != nil {
		return b, mapSQLError(err)
	}
	var e error
	b.CreatedAt, e = parseStoredTime("created_at", created)
	return b, e
}

func (r *EnvironmentBundleRepository) DeleteByApp(ctx context.Context, appName string) error {
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM environment_bundles WHERE app_name = ?`, appName)
	return err
}
