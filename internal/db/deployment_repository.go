package db

import (
	"context"
	"database/sql"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

type DeploymentRepository struct {
	db *Db
}

func NewDeploymentRepository(db *Db) *DeploymentRepository {
	return &DeploymentRepository{db: db}
}

func (r *DeploymentRepository) Create(ctx context.Context, deployment domain.Deployment) error {
	_, err := r.db.db.ExecContext(ctx, `INSERT INTO deployments (id, app_id, status, failure_reason, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		deployment.ID, deployment.AppID, deployment.Status, optionalString(deployment.FailureReason), formatTime(deployment.CreatedAt), formatTime(deployment.UpdatedAt))
	return err
}

func (r *DeploymentRepository) FindByID(ctx context.Context, deploymentID string) (domain.Deployment, error) {
	var deployment domain.Deployment
	var createdAt, updatedAt string
	var failureReason sql.NullString
	err := r.db.db.QueryRowContext(ctx, `SELECT id, app_id, status, failure_reason, created_at, updated_at FROM deployments WHERE id = ?`, deploymentID).
		Scan(&deployment.ID, &deployment.AppID, &deployment.Status, &failureReason, &createdAt, &updatedAt)
	if err != nil {
		return domain.Deployment{}, mapSQLError(err)
	}
	deployment.FailureReason = parseOptionalString(failureReason)
	deployment.CreatedAt = parseStoredTime(createdAt)
	deployment.UpdatedAt = parseStoredTime(updatedAt)
	return deployment, nil
}
