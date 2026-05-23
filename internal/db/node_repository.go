package db

import (
	"context"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type NodeRepository struct {
	db *Db
}

func NewNodeRepository(db *Db) *NodeRepository {
	return &NodeRepository{db: db}
}

func (r *NodeRepository) Create(ctx context.Context, node domain.Node) error {
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO nodes (id, name, status, labels_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		node.ID, node.Name, node.Status, node.LabelsJSON, formatTime(node.CreatedAt), formatTime(node.UpdatedAt))
	return err
}

func (r *NodeRepository) FindByID(ctx context.Context, nodeID string) (domain.Node, error) {
	var node domain.Node
	var createdAt, updatedAt string
	err := r.db.db.QueryRowContext(ctx, `SELECT id, name, status, labels_json, created_at, updated_at FROM nodes WHERE id = ?`, nodeID).
		Scan(&node.ID, &node.Name, &node.Status, &node.LabelsJSON, &createdAt, &updatedAt)
	if err != nil {
		return domain.Node{}, mapSQLError(err)
	}
	node.CreatedAt = parseStoredTime(createdAt)
	node.UpdatedAt = parseStoredTime(updatedAt)
	return node, nil
}
