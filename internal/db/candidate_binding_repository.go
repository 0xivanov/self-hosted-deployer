package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

var ErrCandidateBindingConflict = errors.New("candidate binding conflict")

type CandidateBindingRepository struct{ db *Db }

func NewCandidateBindingRepository(db *Db) *CandidateBindingRepository {
	return &CandidateBindingRepository{db: db}
}

// Begin journals and binds a new candidate atomically. proposedAppID is used
// only when no app row exists; active and deleted rows always retain their
// existing identity.
func (r *CandidateBindingRepository) Begin(ctx context.Context, req domain.DeployRequest, proposedAppID, deploymentID string, now time.Time) (domain.CandidateBinding, bool, error) {
	if strings.TrimSpace(req.AppName) == "" || !deployRequestIDPattern.MatchString(req.RequestID) || strings.TrimSpace(proposedAppID) == "" || strings.TrimSpace(deploymentID) == "" || now.IsZero() {
		return domain.CandidateBinding{}, false, ErrCandidateBindingConflict
	}
	cfg, err := validateCandidateDeployRequest(req)
	if err != nil {
		return domain.CandidateBinding{}, false, err
	}
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return domain.CandidateBinding{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var binding domain.CandidateBinding
	var created string
	err = tx.QueryRowContext(ctx, `SELECT app_name, request_id, app_id, deployment_id, created_at FROM candidate_request_bindings WHERE app_name = ? AND request_id = ?`, req.AppName, req.RequestID).Scan(&binding.AppName, &binding.RequestID, &binding.AppID, &binding.DeploymentID, &created)
	if err == nil {
		var savedState string
		var savedReport int
		if err := tx.QueryRowContext(ctx, `SELECT requested_state, report_withdrawal FROM deployment_requests WHERE app_name = ? AND request_id = ?`, req.AppName, req.RequestID).Scan(&savedState, &savedReport); err != nil {
			return domain.CandidateBinding{}, false, mapSQLError(err)
		}
		if savedState != req.RequestedState || (savedReport != 0) != req.ReportWithdrawal {
			return domain.CandidateBinding{}, false, ErrCandidateBindingConflict
		}
		binding.CreatedAt, err = parseStoredTime("created_at", created)
		if err != nil {
			return domain.CandidateBinding{}, false, err
		}
		return binding, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.CandidateBinding{}, false, mapSQLError(err)
	}
	var pendingApp string
	err = tx.QueryRowContext(ctx, `SELECT app_name FROM deployment_requests WHERE app_name = ? AND state = 'pending' LIMIT 1`, req.AppName).Scan(&pendingApp)
	if err == nil {
		return domain.CandidateBinding{}, false, ErrCandidateBindingConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.CandidateBinding{}, false, mapSQLError(err)
	}
	var existingState string
	var existingReport int
	err = tx.QueryRowContext(ctx, `SELECT state, requested_state, report_withdrawal FROM deployment_requests WHERE app_name = ? AND request_id = ?`, req.AppName, req.RequestID).Scan(&existingState, &created, &existingReport)
	if err == nil {
		return domain.CandidateBinding{}, false, ErrCandidateBindingConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.CandidateBinding{}, false, mapSQLError(err)
	}
	var previousID, previousState sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, desired_state_json FROM apps WHERE name = ? AND deleted_at IS NULL`, req.AppName).Scan(&previousID, &previousState)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return domain.CandidateBinding{}, false, err
	}
	var deletedID, deletedImage, deletedState string
	var deletedAt sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, image, desired_state_json, deleted_at FROM apps WHERE name = ?`, req.AppName).Scan(&deletedID, &deletedImage, &deletedState, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return domain.CandidateBinding{}, false, err
	}
	if previousID.Valid != previousState.Valid {
		return domain.CandidateBinding{}, false, ErrCandidateBindingConflict
	}
	if !previousID.Valid && deletedID != "" && !deletedAt.Valid {
		return domain.CandidateBinding{}, false, ErrCandidateBindingConflict
	}
	if previousID.Valid && (deletedID != previousID.String || deletedAt.Valid || deletedState != previousState.String) {
		return domain.CandidateBinding{}, false, ErrCandidateBindingConflict
	}
	if err := validateCandidateDomainAdmission(ctx, tx, req.AppName, cfg); err != nil {
		return domain.CandidateBinding{}, false, err
	}
	if err := validateCandidatePredecessor(ctx, tx, req.AppName, previousID.String, previousState.String, cfg); err != nil {
		return domain.CandidateBinding{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deployment_requests (app_name, request_id, state, requested_state, previous_app_id, previous_state, report_withdrawal, created_at, updated_at) VALUES (?, ?, 'pending', ?, ?, ?, ?, ?, ?)`, req.AppName, req.RequestID, req.RequestedState, nullableString(previousID), nullableString(previousState), req.ReportWithdrawal, formatTime(now), formatTime(now)); err != nil {
		return domain.CandidateBinding{}, false, ErrCandidateBindingConflict
	}
	actualID := proposedAppID
	if deletedID != "" {
		actualID = deletedID
	} else if previousID.Valid {
		actualID = previousID.String
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO apps (id, name, image, desired_state_json, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, proposedAppID, req.AppName, cfg.Image, req.RequestedState, formatTime(now), formatTime(now), formatTime(now)); err != nil {
		return domain.CandidateBinding{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deployments (id, app_id, status, failure_reason, created_at, updated_at) VALUES (?, ?, 'pending', NULL, ?, ?)`, deploymentID, actualID, formatTime(now), formatTime(now)); err != nil {
		return domain.CandidateBinding{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO candidate_request_bindings (app_name, request_id, app_id, deployment_id, created_at) VALUES (?, ?, ?, ?, ?)`, req.AppName, req.RequestID, actualID, deploymentID, formatTime(now)); err != nil {
		return domain.CandidateBinding{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.CandidateBinding{}, false, err
	}
	return domain.CandidateBinding{AppName: req.AppName, RequestID: req.RequestID, AppID: actualID, DeploymentID: deploymentID, CreatedAt: now}, true, nil
}

// validateCandidateDomainAdmission closes the gap between preflight and the
// durable candidate journal. A live route or another app's pending candidate
// owns a domain until that candidate reaches a terminal state.
func validateCandidateDomainAdmission(ctx context.Context, tx *sql.Tx, appName string, cfg appconfig.Config) error {
	if cfg.Routing.Domain == "" {
		return nil
	}
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT a.name FROM routes r JOIN apps a ON a.id = r.app_id WHERE r.domain = ? AND a.name <> ? LIMIT 1`, cfg.Routing.Domain, appName).Scan(&owner)
	if err == nil {
		return ErrCandidateBindingConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT app_name, requested_state FROM deployment_requests WHERE state = 'pending' AND app_name <> ?`, appName)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var otherApp, requested string
		if err := rows.Scan(&otherApp, &requested); err != nil {
			return err
		}
		other, parseErr := appconfig.FromJSON(requested)
		if parseErr != nil || other.Routing.Domain == cfg.Routing.Domain {
			return ErrCandidateBindingConflict
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func validateCandidateDeployRequest(req domain.DeployRequest) (appconfig.Config, error) {
	cfg, err := appconfig.FromJSON(req.RequestedState)
	canonical, canonicalErr := cfg.JSON()
	if err != nil || canonicalErr != nil || canonical != req.RequestedState || cfg.Name != req.AppName || cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode || len(cfg.Secrets) != 0 || cfg.Validate() != nil {
		return appconfig.Config{}, ErrCandidateBindingConflict
	}
	return cfg, nil
}

func (r *CandidateBindingRepository) Bind(ctx context.Context, appName, requestID, appID, deploymentID string, now time.Time) (domain.CandidateBinding, error) {
	if strings.TrimSpace(appName) == "" || !deployRequestIDPattern.MatchString(requestID) || strings.TrimSpace(appID) == "" || strings.TrimSpace(deploymentID) == "" || now.IsZero() {
		return domain.CandidateBinding{}, ErrCandidateBindingConflict
	}
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return domain.CandidateBinding{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var binding domain.CandidateBinding
	var created string
	err = tx.QueryRowContext(ctx, `SELECT app_name, request_id, app_id, deployment_id, created_at FROM candidate_request_bindings WHERE app_name = ? AND request_id = ?`, appName, requestID).Scan(&binding.AppName, &binding.RequestID, &binding.AppID, &binding.DeploymentID, &created)
	if err == nil {
		binding.CreatedAt, err = parseStoredTime("created_at", created)
		if err != nil {
			return domain.CandidateBinding{}, err
		}
		if binding.AppID != appID || binding.DeploymentID != deploymentID {
			return domain.CandidateBinding{}, ErrCandidateBindingConflict
		}
		return binding, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.CandidateBinding{}, mapSQLError(err)
	}

	var requestState, requestedState string
	var previousAppID, previousState sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT state, requested_state, previous_app_id, previous_state FROM deployment_requests WHERE app_name = ? AND request_id = ?`, appName, requestID).Scan(&requestState, &requestedState, &previousAppID, &previousState)
	if err != nil {
		return domain.CandidateBinding{}, mapSQLError(err)
	}
	if requestState != "pending" {
		return domain.CandidateBinding{}, ErrCandidateBindingConflict
	}
	if previousAppID.Valid != previousState.Valid || (previousAppID.String == "") != (previousState.String == "") {
		return domain.CandidateBinding{}, ErrCandidateBindingConflict
	}
	previousID, previousStateValue := previousAppID.String, previousState.String
	cfg, err := appconfig.FromJSON(requestedState)
	canonical, canonicalErr := cfg.JSON()
	if err != nil || canonicalErr != nil || canonical != requestedState || cfg.Name != appName || cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode || cfg.Validate() != nil {
		return domain.CandidateBinding{}, ErrCandidateBindingConflict
	}
	if err := validateCandidatePredecessor(ctx, tx, appName, previousID, previousStateValue, cfg); err != nil {
		return domain.CandidateBinding{}, err
	}
	var actualID string
	var deletedAt sql.NullString
	var existingImage, existingState string
	err = tx.QueryRowContext(ctx, `SELECT id, image, desired_state_json, deleted_at FROM apps WHERE name = ?`, appName).Scan(&actualID, &existingImage, &existingState, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if previousID != "" {
			return domain.CandidateBinding{}, ErrCandidateBindingConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO apps (id, name, image, desired_state_json, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, appID, appName, cfg.Image, requestedState, formatTime(now), formatTime(now), formatTime(now)); err != nil {
			return domain.CandidateBinding{}, err
		}
		actualID = appID
	} else if err != nil {
		return domain.CandidateBinding{}, mapSQLError(err)
	} else if !deletedAt.Valid {
		if actualID != appID || previousID != actualID || existingState != previousStateValue {
			return domain.CandidateBinding{}, ErrCandidateBindingConflict
		}
	} else if actualID != appID {
		return domain.CandidateBinding{}, ErrCandidateBindingConflict
	}
	if actualID != appID {
		return domain.CandidateBinding{}, ErrCandidateBindingConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deployments (id, app_id, status, failure_reason, created_at, updated_at) VALUES (?, ?, 'pending', NULL, ?, ?)`, deploymentID, actualID, formatTime(now), formatTime(now)); err != nil {
		return domain.CandidateBinding{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO candidate_request_bindings (app_name, request_id, app_id, deployment_id, created_at) VALUES (?, ?, ?, ?, ?)`, appName, requestID, actualID, deploymentID, formatTime(now)); err != nil {
		return domain.CandidateBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.CandidateBinding{}, err
	}
	return domain.CandidateBinding{AppName: appName, RequestID: requestID, AppID: actualID, DeploymentID: deploymentID, CreatedAt: now}, nil
}

func validateCandidatePredecessor(ctx context.Context, tx *sql.Tx, appName, previousID, previousState string, candidate appconfig.Config) error {
	var id, state string
	var deletedAt sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, desired_state_json, deleted_at FROM apps WHERE name = ?`, appName).Scan(&id, &state, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if previousID != "" {
			return ErrCandidateBindingConflict
		}
		return nil
	}
	if err != nil {
		return err
	}
	if previousID == "" {
		if !deletedAt.Valid {
			return ErrCandidateBindingConflict
		}
		return nil
	}
	if deletedAt.Valid || id != previousID || state != previousState {
		return ErrCandidateBindingConflict
	}
	predecessor, err := appconfig.FromJSON(state)
	if err != nil {
		return ErrCandidateBindingConflict
	}
	canonical, err := predecessor.JSON()
	if err != nil || canonical != state || predecessor.Name != appName || predecessor.Hosting == nil || predecessor.State.Mode != appconfig.DefaultStateMode || predecessor.Service.Port != candidate.Service.Port || predecessor.Validate() != nil {
		return ErrCandidateBindingConflict
	}
	return nil
}

func (r *CandidateBindingRepository) Find(ctx context.Context, appName, requestID string) (domain.CandidateBinding, error) {
	var binding domain.CandidateBinding
	var created string
	if strings.TrimSpace(appName) == "" || !deployRequestIDPattern.MatchString(requestID) {
		return binding, ErrCandidateBindingConflict
	}
	err := r.db.conn.QueryRowContext(ctx, `SELECT app_name, request_id, app_id, deployment_id, created_at FROM candidate_request_bindings WHERE app_name = ? AND request_id = ?`, appName, requestID).Scan(&binding.AppName, &binding.RequestID, &binding.AppID, &binding.DeploymentID, &created)
	if err != nil {
		return binding, mapSQLError(err)
	}
	binding.CreatedAt, err = parseStoredTime("created_at", created)
	return binding, err
}
