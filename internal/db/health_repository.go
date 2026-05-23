package db

import "context"

type HealthRepository struct {
	db *Db
}

func NewHealthRepository(db *Db) *HealthRepository {
	return &HealthRepository{db: db}
}

func (r *HealthRepository) Ping(ctx context.Context) error {
	return r.db.Ping(ctx)
}
