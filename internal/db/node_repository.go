package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type NodeRepository struct {
	db *Db
}

func NewNodeRepository(db *Db) *NodeRepository {
	return &NodeRepository{db: db}
}

func (r *NodeRepository) Create(ctx context.Context, node domain.Node) error {
	if node.Status == "" {
		node.Status = "pending"
	}
	if node.LabelsJSON == "" {
		node.LabelsJSON = "{}"
	}
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO nodes (id, name, status, labels_json, hostname, arch, os, kernel, last_seen_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.ID, node.Name, node.Status, node.LabelsJSON, node.Hostname, node.Arch, node.OS, node.Kernel, formatOptionalTime(node.LastSeenAt), formatTime(node.CreatedAt), formatTime(node.UpdatedAt))
	return err
}

func (r *NodeRepository) FindByID(ctx context.Context, nodeID string) (domain.Node, error) {
	return r.findOne(ctx, `SELECT id, name, status, labels_json, hostname, arch, os, kernel, last_seen_at, created_at, updated_at FROM nodes WHERE id = ?`, nodeID)
}

func (r *NodeRepository) FindByName(ctx context.Context, name string) (domain.Node, error) {
	return r.findOne(ctx, `SELECT id, name, status, labels_json, hostname, arch, os, kernel, last_seen_at, created_at, updated_at FROM nodes WHERE name = ?`, name)
}

func (r *NodeRepository) List(ctx context.Context) ([]domain.Node, error) {
	rows, err := r.db.db.QueryContext(ctx, `SELECT id, name, status, labels_json, hostname, arch, os, kernel, last_seen_at, created_at, updated_at FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (r *NodeRepository) UpdateStatus(ctx context.Context, nodeID string, status string, updatedAt time.Time) error {
	return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE nodes SET status = ?, updated_at = ? WHERE id = ?`, status, formatTime(updatedAt), nodeID))
}

func (r *NodeRepository) UpdateLastSeen(ctx context.Context, nodeID string, seenAt time.Time) error {
	return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE nodes SET last_seen_at = ?, status = ?, updated_at = ? WHERE id = ?`, formatTime(seenAt), "online", formatTime(seenAt), nodeID))
}

func (r *NodeRepository) UpdateHeartbeat(ctx context.Context, nodeID string, heartbeat domain.Node, seenAt time.Time) error {
	if heartbeat.LabelsJSON != "" {
		return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE nodes SET status = ?, labels_json = ?, hostname = ?, arch = ?, os = ?, kernel = ?, last_seen_at = ?, updated_at = ? WHERE id = ?`,
			heartbeat.Status, heartbeat.LabelsJSON, heartbeat.Hostname, heartbeat.Arch, heartbeat.OS, heartbeat.Kernel, formatTime(seenAt), formatTime(seenAt), nodeID))
	}
	return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE nodes SET status = ?, hostname = ?, arch = ?, os = ?, kernel = ?, last_seen_at = ?, updated_at = ? WHERE id = ?`,
		heartbeat.Status, heartbeat.Hostname, heartbeat.Arch, heartbeat.OS, heartbeat.Kernel, formatTime(seenAt), formatTime(seenAt), nodeID))
}

func (r *NodeRepository) findOne(ctx context.Context, query string, args ...any) (domain.Node, error) {
	row := r.db.db.QueryRowContext(ctx, query, args...)
	node, err := scanNode(row)
	if err != nil {
		return domain.Node{}, mapSQLError(err)
	}
	return node, nil
}

type nodeScanner interface {
	Scan(dest ...any) error
}

func scanNode(scanner nodeScanner) (domain.Node, error) {
	var node domain.Node
	var createdAt, updatedAt string
	var lastSeenAt sql.NullString
	err := scanner.Scan(&node.ID, &node.Name, &node.Status, &node.LabelsJSON, &node.Hostname, &node.Arch, &node.OS, &node.Kernel, &lastSeenAt, &createdAt, &updatedAt)
	if err != nil {
		return domain.Node{}, err
	}
	node.LastSeenAt = parseOptionalStoredTime(lastSeenAt)
	node.CreatedAt = parseStoredTime(createdAt)
	node.UpdatedAt = parseStoredTime(updatedAt)
	return node, nil
}
