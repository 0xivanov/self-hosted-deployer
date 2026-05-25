package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type AppRepository struct {
	db *Db
}

func NewAppRepository(db *Db) *AppRepository {
	return &AppRepository{db: db}
}

func (r *AppRepository) Create(ctx context.Context, app domain.App) error {
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO apps (id, name, image, desired_state_json, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		app.ID, app.Name, app.Image, app.DesiredStateJSON, formatTime(app.CreatedAt), formatTime(app.UpdatedAt), formatOptionalTime(app.DeletedAt))
	return err
}

func (r *AppRepository) Update(ctx context.Context, app domain.App) error {
	return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE apps SET image = ?, desired_state_json = ?, updated_at = ?, deleted_at = ? WHERE id = ?`,
		app.Image, app.DesiredStateJSON, formatTime(app.UpdatedAt), formatOptionalTime(app.DeletedAt), app.ID))
}

func (r *AppRepository) FindByName(ctx context.Context, name string) (domain.App, error) {
	return r.findOne(ctx, `SELECT id, name, image, desired_state_json, created_at, updated_at, deleted_at FROM apps WHERE name = ?`, name)
}

func (r *AppRepository) FindByID(ctx context.Context, appID string) (domain.App, error) {
	return r.findOne(ctx, `SELECT id, name, image, desired_state_json, created_at, updated_at, deleted_at FROM apps WHERE id = ?`, appID)
}

func (r *AppRepository) findOne(ctx context.Context, query string, args ...any) (domain.App, error) {
	var app domain.App
	var createdAt, updatedAt string
	var deletedAt sql.NullString
	err := r.db.db.QueryRowContext(ctx, query, args...).
		Scan(&app.ID, &app.Name, &app.Image, &app.DesiredStateJSON, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return domain.App{}, mapSQLError(err)
	}
	app.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.App{}, err
	}
	app.UpdatedAt, err = parseStoredTime("updated_at", updatedAt)
	if err != nil {
		return domain.App{}, err
	}
	app.DeletedAt, err = parseOptionalStoredTime("deleted_at", deletedAt)
	if err != nil {
		return domain.App{}, err
	}
	return app, nil
}

func (r *AppRepository) FindActiveByName(ctx context.Context, name string) (domain.App, error) {
	app, err := r.FindByName(ctx, name)
	if err != nil {
		return domain.App{}, err
	}
	if app.DeletedAt != nil {
		return domain.App{}, ErrNotFound
	}
	return app, nil
}

func (r *AppRepository) List(ctx context.Context) ([]domain.App, error) {
	rows, err := r.db.db.QueryContext(ctx, `SELECT id, name, image, desired_state_json, created_at, updated_at, deleted_at FROM apps WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	apps := []domain.App{}
	for rows.Next() {
		app, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return apps, nil
}

func (r *AppRepository) MarkDeleted(ctx context.Context, name string, deletedAt time.Time) (domain.App, error) {
	result, err := r.db.db.ExecContext(ctx, `UPDATE apps SET updated_at = ?, deleted_at = ? WHERE name = ? AND deleted_at IS NULL`,
		formatTime(deletedAt), formatTime(deletedAt), name)
	if err := mapRowsAffected(result, err); err != nil {
		return domain.App{}, err
	}
	return r.FindByName(ctx, name)
}

type appScanner interface {
	Scan(dest ...any) error
}

func scanApp(scanner appScanner) (domain.App, error) {
	var app domain.App
	var createdAt, updatedAt string
	var deletedAt sql.NullString
	err := scanner.Scan(&app.ID, &app.Name, &app.Image, &app.DesiredStateJSON, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return domain.App{}, mapSQLError(err)
	}
	app.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.App{}, err
	}
	app.UpdatedAt, err = parseStoredTime("updated_at", updatedAt)
	if err != nil {
		return domain.App{}, err
	}
	app.DeletedAt, err = parseOptionalStoredTime("deleted_at", deletedAt)
	if err != nil {
		return domain.App{}, err
	}
	return app, nil
}
