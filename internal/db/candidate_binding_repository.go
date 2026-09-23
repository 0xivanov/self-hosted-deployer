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
	if err := validateCandidatePredecessor(ctx, tx, appName, previousID, previousStateValue); err != nil {
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

func validateCandidatePredecessor(ctx context.Context, tx *sql.Tx, appName, previousID, previousState string) error {
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
