package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/0xivanov/self-hosted-deployer/internal/repository/sqlite/migrations"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

type Repository struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*Repository, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	repo := &Repository{db: db}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if err := repo.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repo, nil
}

func (r *Repository) Close() error {
	return r.db.Close()
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

func (r *Repository) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.UpContext(ctx, r.db, "."); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	return nil
}
