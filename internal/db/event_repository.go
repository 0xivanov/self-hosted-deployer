package db

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

const (
	defaultEventListLimit = 100
	maxEventListLimit     = 1000
)

type EventRepository struct {
	db *Db
}

func NewEventRepository(db *Db) *EventRepository {
	return &EventRepository{db: db}
}

func (r *EventRepository) Create(ctx context.Context, event domain.Event) error {
	if event.MetadataJSON == "" {
		event.MetadataJSON = "{}"
	}
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO events (id, created_at, type, severity, message, app_id, node_id, deployment_id, metadata_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, formatTime(event.CreatedAt), event.Type, event.Severity, event.Message, optionalString(event.AppID), optionalString(event.NodeID), optionalString(event.DeploymentID), event.MetadataJSON)
	return err
}

func (r *EventRepository) List(ctx context.Context, filter domain.EventFilter) ([]domain.Event, error) {
	query := `SELECT id, created_at, type, severity, message, app_id, node_id, deployment_id, metadata_json FROM events`
	conditions := []string{}
	args := []any{}
	if filter.AppID != "" {
		conditions = append(conditions, "app_id = ?")
		args = append(args, filter.AppID)
	}
	if filter.NodeID != "" {
		conditions = append(conditions, "node_id = ?")
		args = append(args, filter.NodeID)
	}
	if filter.Type != "" {
		conditions = append(conditions, "type = ?")
		args = append(args, filter.Type)
	}
	if filter.Severity != "" {
		conditions = append(conditions, "severity = ?")
		args = append(args, filter.Severity)
	}
	if filter.Since != nil {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, formatTime(*filter.Since))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultEventListLimit
	}
	if limit > maxEventListLimit {
		limit = maxEventListLimit
	}
	args = append(args, limit)

	rows, err := r.db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []domain.Event{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *EventRepository) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.db.ExecContext(ctx, `DELETE FROM events WHERE created_at < ?`, formatTime(cutoff))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *EventRepository) PruneToLimit(ctx context.Context, maxCount int) (int64, error) {
	if maxCount < 1 {
		return 0, nil
	}
	result, err := r.db.db.ExecContext(ctx, `DELETE FROM events WHERE id IN (SELECT id FROM events ORDER BY created_at DESC, id DESC LIMIT -1 OFFSET ?)`, maxCount)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

type eventScanner interface {
	Scan(dest ...any) error
}

func scanEvent(scanner eventScanner) (domain.Event, error) {
	var event domain.Event
	var createdAt string
	var appID, nodeID, deploymentID sql.NullString
	err := scanner.Scan(&event.ID, &createdAt, &event.Type, &event.Severity, &event.Message, &appID, &nodeID, &deploymentID, &event.MetadataJSON)
	if err != nil {
		return domain.Event{}, err
	}
	event.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.Event{}, err
	}
	event.AppID = parseOptionalString(appID)
	event.NodeID = parseOptionalString(nodeID)
	event.DeploymentID = parseOptionalString(deploymentID)
	return event, nil
}
