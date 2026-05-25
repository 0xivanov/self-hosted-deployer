package db

import (
	"context"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type RouteRepository struct {
	db *Db
}

func NewRouteRepository(db *Db) *RouteRepository {
	return &RouteRepository{db: db}
}

func (r *RouteRepository) Create(ctx context.Context, route domain.Route) error {
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO routes (id, app_id, domain, target_port, status, tls_enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		route.ID, route.AppID, route.Domain, route.TargetPort, route.Status, boolToInt(route.TLSEnabled), formatTime(route.CreatedAt), formatTime(route.UpdatedAt))
	return err
}

func (r *RouteRepository) UpsertForApp(ctx context.Context, route domain.Route) error {
	_, err := r.db.db.ExecContext(ctx, `
		INSERT INTO routes (id, app_id, domain, target_port, status, tls_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(app_id) DO UPDATE SET
			domain = excluded.domain,
			target_port = excluded.target_port,
			status = excluded.status,
			tls_enabled = excluded.tls_enabled,
			updated_at = excluded.updated_at`,
		route.ID, route.AppID, route.Domain, route.TargetPort, route.Status, boolToInt(route.TLSEnabled), formatTime(route.CreatedAt), formatTime(route.UpdatedAt))
	return err
}

func (r *RouteRepository) DeleteByApp(ctx context.Context, appID string) error {
	_, err := r.db.db.ExecContext(ctx, `DELETE FROM routes WHERE app_id = ?`, appID)
	return err
}

func (r *RouteRepository) FindByDomain(ctx context.Context, domainName string) (domain.Route, error) {
	return r.findOne(ctx, `SELECT id, app_id, domain, target_port, status, tls_enabled, created_at, updated_at FROM routes WHERE domain = ?`, domainName)
}

func (r *RouteRepository) List(ctx context.Context) ([]domain.Route, error) {
	rows, err := r.db.db.QueryContext(ctx, `SELECT id, app_id, domain, target_port, status, tls_enabled, created_at, updated_at FROM routes ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := []domain.Route{}
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return routes, nil
}

func (r *RouteRepository) ListByApp(ctx context.Context, appID string) ([]domain.Route, error) {
	rows, err := r.db.db.QueryContext(ctx, `SELECT id, app_id, domain, target_port, status, tls_enabled, created_at, updated_at FROM routes WHERE app_id = ? ORDER BY domain`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := []domain.Route{}
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return routes, nil
}

func (r *RouteRepository) UpdateStatus(ctx context.Context, routeID string, status string, updatedAt time.Time) error {
	return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE routes SET status = ?, updated_at = ? WHERE id = ?`, status, formatTime(updatedAt), routeID))
}

func (r *RouteRepository) findOne(ctx context.Context, query string, args ...any) (domain.Route, error) {
	row := r.db.db.QueryRowContext(ctx, query, args...)
	route, err := scanRoute(row)
	if err != nil {
		return domain.Route{}, mapSQLError(err)
	}
	return route, nil
}

type routeScanner interface {
	Scan(dest ...any) error
}

func scanRoute(scanner routeScanner) (domain.Route, error) {
	var route domain.Route
	var createdAt, updatedAt string
	var tlsEnabled int
	err := scanner.Scan(&route.ID, &route.AppID, &route.Domain, &route.TargetPort, &route.Status, &tlsEnabled, &createdAt, &updatedAt)
	if err != nil {
		return domain.Route{}, mapSQLError(err)
	}
	route.TLSEnabled = tlsEnabled != 0
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

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
