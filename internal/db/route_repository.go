package db

import (
	"context"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type RouteRepository struct {
	db *Db
}

func NewRouteRepository(db *Db) *RouteRepository {
	return &RouteRepository{db: db}
}

func (r *RouteRepository) Create(ctx context.Context, route domain.Route) error {
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO routes (id, app_id, domain, target_port, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		route.ID, route.AppID, route.Domain, route.TargetPort, formatTime(route.CreatedAt), formatTime(route.UpdatedAt))
	return err
}

func (r *RouteRepository) FindByDomain(ctx context.Context, domainName string) (domain.Route, error) {
	var route domain.Route
	var createdAt, updatedAt string
	err := r.db.db.QueryRowContext(ctx, `SELECT id, app_id, domain, target_port, created_at, updated_at FROM routes WHERE domain = ?`, domainName).
		Scan(&route.ID, &route.AppID, &route.Domain, &route.TargetPort, &createdAt, &updatedAt)
	if err != nil {
		return domain.Route{}, mapSQLError(err)
	}
	route.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.Route{}, err
	}
	route.UpdatedAt, err = parseStoredTime("updated_at", updatedAt)
	if err != nil {
		return domain.Route{}, err
	}
	return route, nil
}
