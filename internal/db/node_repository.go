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
	_, err := r.db.conn.ExecContext(ctx, `INSERT INTO nodes (id, name, status, labels_json, hostname, arch, os, kernel, wireguard_ip, wireguard_public_key, last_seen_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.ID, node.Name, node.Status, node.LabelsJSON, node.Hostname, node.Arch, node.OS, node.Kernel, node.WireGuardIP, node.WireGuardPublicKey, formatOptionalTime(node.LastSeenAt), formatTime(node.CreatedAt), formatTime(node.UpdatedAt))
	return err
}

func (r *NodeRepository) FindByID(ctx context.Context, nodeID string) (domain.Node, error) {
	return r.findOne(ctx, `SELECT id, name, status, labels_json, hostname, arch, os, kernel, wireguard_ip, wireguard_public_key, last_seen_at, created_at, updated_at FROM nodes WHERE id = ?`, nodeID)
}

func (r *NodeRepository) FindByName(ctx context.Context, name string) (domain.Node, error) {
	return r.findOne(ctx, `SELECT id, name, status, labels_json, hostname, arch, os, kernel, wireguard_ip, wireguard_public_key, last_seen_at, created_at, updated_at FROM nodes WHERE name = ?`, name)
}

func (r *NodeRepository) List(ctx context.Context) ([]domain.Node, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT id, name, status, labels_json, hostname, arch, os, kernel, wireguard_ip, wireguard_public_key, last_seen_at, created_at, updated_at FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := []domain.Node{}
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
	return mapRowsAffected(r.db.conn.ExecContext(ctx, `UPDATE nodes SET status = ?, updated_at = ? WHERE id = ?`, status, formatTime(updatedAt), nodeID))
}

func (r *NodeRepository) UpdateLastSeen(ctx context.Context, nodeID string, seenAt time.Time) error {
	return mapRowsAffected(r.db.conn.ExecContext(ctx, `UPDATE nodes SET last_seen_at = ?, status = ?, updated_at = ? WHERE id = ?`, formatTime(seenAt), "online", formatTime(seenAt), nodeID))
}

func (r *NodeRepository) UpdateHeartbeat(ctx context.Context, nodeID string, heartbeat domain.Node, seenAt time.Time) error {
	if heartbeat.LabelsJSON != "" {
		return mapRowsAffected(r.db.conn.ExecContext(ctx, `UPDATE nodes SET status = ?, labels_json = ?, hostname = ?, arch = ?, os = ?, kernel = ?, last_seen_at = ?, updated_at = ? WHERE id = ?`,
			heartbeat.Status, heartbeat.LabelsJSON, heartbeat.Hostname, heartbeat.Arch, heartbeat.OS, heartbeat.Kernel, formatTime(seenAt), formatTime(seenAt), nodeID))
	}
	return mapRowsAffected(r.db.conn.ExecContext(ctx, `UPDATE nodes SET status = ?, hostname = ?, arch = ?, os = ?, kernel = ?, last_seen_at = ?, updated_at = ? WHERE id = ?`,
		heartbeat.Status, heartbeat.Hostname, heartbeat.Arch, heartbeat.OS, heartbeat.Kernel, formatTime(seenAt), formatTime(seenAt), nodeID))
}

func (r *NodeRepository) SetWireGuard(ctx context.Context, nodeID string, wireGuardIP string, publicKey string, updatedAt time.Time) error {
	return mapRowsAffected(r.db.conn.ExecContext(ctx, `UPDATE nodes SET wireguard_ip = ?, wireguard_public_key = ?, updated_at = ? WHERE id = ?`,
		wireGuardIP, publicKey, formatTime(updatedAt), nodeID))
}

func (r *NodeRepository) findOne(ctx context.Context, query string, args ...any) (domain.Node, error) {
	row := r.db.conn.QueryRowContext(ctx, query, args...)
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
	err := scanner.Scan(&node.ID, &node.Name, &node.Status, &node.LabelsJSON, &node.Hostname, &node.Arch, &node.OS, &node.Kernel, &node.WireGuardIP, &node.WireGuardPublicKey, &lastSeenAt, &createdAt, &updatedAt)
	if err != nil {
		return domain.Node{}, err
	}
	node.LastSeenAt, err = parseOptionalStoredTime("last_seen_at", lastSeenAt)
	if err != nil {
		return domain.Node{}, err
	}
	node.CreatedAt, err = parseStoredTime("created_at", createdAt)
	if err != nil {
		return domain.Node{}, err
	}
	node.UpdatedAt, err = parseStoredTime("updated_at", updatedAt)
	if err != nil {
		return domain.Node{}, err
	}
	return node, nil
}
