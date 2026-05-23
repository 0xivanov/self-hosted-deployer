package db

import (
	"context"
	"database/sql"

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

func (r *AppRepository) FindByName(ctx context.Context, name string) (domain.App, error) {
	var app domain.App
	var createdAt, updatedAt string
	var deletedAt sql.NullString
	err := r.db.db.QueryRowContext(ctx, `SELECT id, name, image, desired_state_json, created_at, updated_at, deleted_at FROM apps WHERE name = ?`, name).
		Scan(&app.ID, &app.Name, &app.Image, &app.DesiredStateJSON, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return domain.App{}, mapSQLError(err)
	}
	app.CreatedAt = parseStoredTime(createdAt)
	app.UpdatedAt = parseStoredTime(updatedAt)
	app.DeletedAt = parseOptionalStoredTime(deletedAt)
	return app, nil
}
