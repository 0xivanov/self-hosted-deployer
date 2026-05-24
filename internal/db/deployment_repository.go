package db

import (
	"context"
	"database/sql"
	"time"

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

func (r *DeploymentRepository) UpdateStatus(ctx context.Context, deploymentID string, status string, failureReason string, updatedAt time.Time) error {
	return mapRowsAffected(r.db.db.ExecContext(ctx, `UPDATE deployments SET status = ?, failure_reason = ?, updated_at = ? WHERE id = ?`,
		status, optionalString(failureReason), formatTime(updatedAt), deploymentID))
}

func (r *DeploymentRepository) ListByApp(ctx context.Context, appID string) ([]domain.Deployment, error) {
	rows, err := r.db.db.QueryContext(ctx, `SELECT id, app_id, status, failure_reason, created_at, updated_at FROM deployments WHERE app_id = ? ORDER BY created_at DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deployments []domain.Deployment
	for rows.Next() {
		deployment, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, deployment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deployments, nil
}

type deploymentScanner interface {
	Scan(dest ...any) error
}

func scanDeployment(scanner deploymentScanner) (domain.Deployment, error) {
	var deployment domain.Deployment
	var createdAt, updatedAt string
	var failureReason sql.NullString
	err := scanner.Scan(&deployment.ID, &deployment.AppID, &deployment.Status, &failureReason, &createdAt, &updatedAt)
	if err != nil {
		return domain.Deployment{}, mapSQLError(err)
	}
	deployment.FailureReason = parseOptionalString(failureReason)
	deployment.CreatedAt = parseStoredTime(createdAt)
	deployment.UpdatedAt = parseStoredTime(updatedAt)
	return deployment, nil
}
