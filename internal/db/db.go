package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/0xivanov/self-hosted-deployer/internal/db/migrations"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

type Db struct {
	conn *sql.DB
}

func Open(ctx context.Context, dsn string) (*Db, error) {
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	database := New(sqlDB)
	if _, err := sqlDB.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		if closeErr := sqlDB.Close(); closeErr != nil {
			return nil, errors.Join(
				fmt.Errorf("enable sqlite foreign keys: %w", err),
				fmt.Errorf("close sqlite: %w", closeErr),
			)
		}
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if err := database.Migrate(ctx); err != nil {
		if closeErr := sqlDB.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close sqlite: %w", closeErr))
		}
		return nil, err
	}
	return database, nil
}

func New(sqlDB *sql.DB) *Db {
	return &Db{conn: sqlDB}
}

func (r *Db) Close() error {
	return r.conn.Close()
}

func (r *Db) Ping(ctx context.Context) error {
	return r.conn.PingContext(ctx)
}

func (r *Db) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.UpContext(ctx, r.conn, "."); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	return nil
}
