package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

var ErrDeployRequestPending = errors.New("deployment request is pending")
var ErrDeployRequestIdentity = errors.New("deployment request identity mismatch")
var deployRequestIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type DeploymentRequestRepository struct{ db *Db }

func NewDeploymentRequestRepository(db *Db) *DeploymentRequestRepository {
	return &DeploymentRequestRepository{db: db}
}
func (r *DeploymentRequestRepository) Begin(ctx context.Context, req domain.DeployRequest) (domain.DeployRequest, bool, error) {
	if req.AppName == "" || !deployRequestIDPattern.MatchString(req.RequestID) || !json.Valid([]byte(req.RequestedState)) {
		return domain.DeployRequest{}, false, ErrDeployRequestIdentity
	}
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return domain.DeployRequest{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var existing domain.DeployRequest
	var report int
	var response, previousID, previousState *string
	var created, updated string
	err = tx.QueryRowContext(ctx, `SELECT app_name, request_id, state, requested_state, previous_app_id, previous_state, report_withdrawal, response_json, created_at, updated_at FROM deployment_requests WHERE app_name = ? AND request_id = ?`, req.AppName, req.RequestID).Scan(&existing.AppName, &existing.RequestID, &existing.State, &existing.RequestedState, &previousID, &previousState, &report, &response, &created, &updated)
	if err == nil {
		existing.ReportWithdrawal = report != 0
		if previousID != nil {
			existing.PreviousAppID = *previousID
		}
		if previousState != nil {
			existing.PreviousState = *previousState
		}
		if response != nil {
			existing.ResponseJSON = *response
		}
		existing.CreatedAt, err = parseStoredTime("created_at", created)
		if err == nil {
			existing.UpdatedAt, err = parseStoredTime("updated_at", updated)
		}
		if err != nil {
			return domain.DeployRequest{}, false, err
		}
		if existing.RequestedState != req.RequestedState || existing.ReportWithdrawal != req.ReportWithdrawal {
			return domain.DeployRequest{}, false, ErrDeployRequestIdentity
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.DeployRequest{}, false, mapSQLError(err)
	}
	// Capture the predecessor and journal insertion in one transaction. This
	// makes the predecessor durable before any caller can mutate the app.
	var previousAppID, previousDesiredState sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, desired_state_json FROM apps WHERE name = ? AND deleted_at IS NULL`, req.AppName).Scan(&previousAppID, &previousDesiredState)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return domain.DeployRequest{}, false, mapSQLError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO deployment_requests (app_name, request_id, state, requested_state, previous_app_id, previous_state, report_withdrawal, created_at, updated_at) VALUES (?, ?, 'pending', ?, ?, ?, ?, ?, ?)`, req.AppName, req.RequestID, req.RequestedState, nullableString(previousAppID), nullableString(previousDesiredState), req.ReportWithdrawal, formatTime(req.CreatedAt), formatTime(req.UpdatedAt))
	if err != nil {
		return domain.DeployRequest{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DeployRequest{}, false, err
	}
	req.PreviousAppID = previousAppID.String
	req.PreviousState = previousDesiredState.String
	return req, true, nil
}

func nullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
func (r *DeploymentRequestRepository) Find(ctx context.Context, appName, requestID string) (domain.DeployRequest, error) {
	var req domain.DeployRequest
	var report int
	var response, previousID, previousState *string
	var created, updated string
	err := r.db.conn.QueryRowContext(ctx, `SELECT app_name, request_id, state, requested_state, previous_app_id, previous_state, report_withdrawal, response_json, created_at, updated_at FROM deployment_requests WHERE app_name = ? AND request_id = ?`, appName, requestID).Scan(&req.AppName, &req.RequestID, &req.State, &req.RequestedState, &previousID, &previousState, &report, &response, &created, &updated)
	if err != nil {
		return req, mapSQLError(err)
	}
	req.ReportWithdrawal = report != 0
	if previousID != nil {
		req.PreviousAppID = *previousID
	}
	if previousState != nil {
		req.PreviousState = *previousState
	}
	if response != nil {
		req.ResponseJSON = *response
	}
	req.CreatedAt, err = parseStoredTime("created_at", created)
	if err != nil {
		return req, err
	}
	req.UpdatedAt, err = parseStoredTime("updated_at", updated)
	return req, err
}
func (r *DeploymentRequestRepository) Complete(ctx context.Context, appName, requestID, state, response string, updated time.Time) error {
	if (state != "applied" && state != "withdrawn") || !json.Valid([]byte(response)) || response == "null" {
		return ErrDeployRequestIdentity
	}
	// Bound candidates must commit app, deployment, route and receipt together.
	// Preserve this legacy completion path only for requests without a binding.
	return mapRowsAffected(r.db.conn.ExecContext(ctx, `UPDATE deployment_requests SET state = ?, response_json = ?, updated_at = ? WHERE app_name = ? AND request_id = ? AND state = 'pending' AND NOT EXISTS (SELECT 1 FROM candidate_request_bindings b WHERE b.app_name = deployment_requests.app_name AND b.request_id = deployment_requests.request_id)`, state, response, formatTime(updated), appName, requestID))
}
func (r *DeploymentRequestRepository) PendingByApp(ctx context.Context, appName string) (domain.DeployRequest, error) {
	var req domain.DeployRequest
	var report int
	var response, previousID, previousState *string
	var created, updated string
	err := r.db.conn.QueryRowContext(ctx, `SELECT app_name, request_id, state, requested_state, previous_app_id, previous_state, report_withdrawal, response_json, created_at, updated_at FROM deployment_requests WHERE app_name = ? AND state = 'pending'`, appName).Scan(&req.AppName, &req.RequestID, &req.State, &req.RequestedState, &previousID, &previousState, &report, &response, &created, &updated)
	if err != nil {
		return req, mapSQLError(err)
	}
	req.ReportWithdrawal = report != 0
	if previousID != nil {
		req.PreviousAppID = *previousID
	}
	if previousState != nil {
		req.PreviousState = *previousState
	}
	if response != nil {
		req.ResponseJSON = *response
	}
	req.CreatedAt, err = parseStoredTime("created_at", created)
	if err != nil {
		return req, err
	}
	req.UpdatedAt, err = parseStoredTime("updated_at", updated)
	return req, err
}
