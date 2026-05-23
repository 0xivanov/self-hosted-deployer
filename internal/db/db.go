package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/0xivanov/self-hosted-deployer/internal/db/migrations"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

type Db struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*Db, error) {
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	database := New(sqlDB)
	if _, err := sqlDB.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if err := database.Migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return database, nil
}

func New(sqlDB *sql.DB) *Db {
	return &Db{db: sqlDB}
}

func (r *Db) Close() error {
	return r.db.Close()
}

func (r *Db) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

func (r *Db) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.UpContext(ctx, r.db, "."); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	return nil
}
