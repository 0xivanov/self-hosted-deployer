package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

var (
	ErrRuntimeCheckpointConflict   = errors.New("runtime activation checkpoint conflict")
	ErrRuntimeCheckpointTerminal   = errors.New("runtime activation checkpoint is terminal")
	ErrRuntimeCheckpointTransition = errors.New("invalid runtime activation checkpoint transition")
	ErrRuntimeCheckpointIdentity   = errors.New("invalid runtime activation checkpoint identity")
)

const runtimeCheckpointJSONLimit = 64 * 1024

type RuntimeCheckpointRepository struct{ db *Db }

func NewRuntimeCheckpointRepository(db *Db) *RuntimeCheckpointRepository {
	return &RuntimeCheckpointRepository{db: db}
}

func validateCheckpointObject(value string) error {
	if len(value) == 0 || len(value) > runtimeCheckpointJSONLimit || !json.Valid([]byte(value)) {
		return ErrRuntimeCheckpointIdentity
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &object); err != nil || object == nil {
		return ErrRuntimeCheckpointIdentity
	}
	return nil
}

func validateCheckpointIdentity(appName, requestID string) error {
	if strings.TrimSpace(appName) == "" || !deployRequestIDPattern.MatchString(requestID) {
		return ErrRuntimeCheckpointIdentity
	}
	return nil
}

func (r *RuntimeCheckpointRepository) SaveActivationIntent(ctx context.Context, appName, requestID, intentJSON string) error {
	if err := validateCheckpointIdentity(appName, requestID); err != nil {
		return err
	}
	if err := validateCheckpointObject(intentJSON); err != nil {
		return err
	}
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var parentState string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM deployment_requests WHERE app_name = ? AND request_id = ?`, appName, requestID).Scan(&parentState); err != nil {
		return mapSQLError(err)
	}
	if parentState != "pending" {
		return ErrRuntimeCheckpointTerminal
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT intent_json FROM runtime_activation_checkpoints WHERE app_name = ? AND request_id = ?`, appName, requestID).Scan(&existing)
	if err == nil {
		if existing != intentJSON {
			return ErrRuntimeCheckpointConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := formatTime(time.Now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_activation_checkpoints (app_name, request_id, stage, intent_json, gate_json, created_at, updated_at) VALUES (?, ?, 'prepared', ?, '{}', ?, ?)`, appName, requestID, intentJSON, now, now); err != nil {
		return err
	}
	return tx.Commit()
}

func validCheckpointTransition(expected, next string) bool {
	switch {
	case next == "fenced" && expected == "prepared":
	case next == "activated" && expected == "fenced":
	case next == "recovering" && (expected == "prepared" || expected == "fenced" || expected == "activated"):
	case next == "withdrawn" && expected == "recovering":
	default:
		return false
	}
	return true
}

func (r *RuntimeCheckpointRepository) RecordActivationGate(ctx context.Context, appName, requestID, expectedStage, nextStage, gateJSON string) error {
	if err := validateCheckpointIdentity(appName, requestID); err != nil {
		return err
	}
	if err := validateCheckpointObject(gateJSON); err != nil {
		return err
	}
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var parentState, currentStage string
	if err := tx.QueryRowContext(ctx, `SELECT d.state, c.stage FROM deployment_requests d JOIN runtime_activation_checkpoints c ON c.app_name = d.app_name AND c.request_id = d.request_id WHERE d.app_name = ? AND d.request_id = ?`, appName, requestID).Scan(&parentState, &currentStage); err != nil {
		return mapSQLError(err)
	}
	if parentState != "pending" || currentStage == "withdrawn" {
		return ErrRuntimeCheckpointTerminal
	}
	if expectedStage == "" || !validCheckpointTransition(expectedStage, nextStage) {
		return ErrRuntimeCheckpointTransition
	}
	if currentStage != expectedStage {
		return ErrRuntimeCheckpointConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_activation_checkpoints SET stage = ?, gate_json = ?, updated_at = ? WHERE app_name = ? AND request_id = ? AND stage = ?`, nextStage, gateJSON, formatTime(time.Now()), appName, requestID, expectedStage)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrRuntimeCheckpointConflict
	}
	return tx.Commit()
}

func (r *RuntimeCheckpointRepository) FindRuntimeCheckpoint(ctx context.Context, appName, requestID string) (domain.RuntimeCheckpoint, error) {
	var checkpoint domain.RuntimeCheckpoint
	if err := validateCheckpointIdentity(appName, requestID); err != nil {
		return checkpoint, err
	}
	err := r.db.conn.QueryRowContext(ctx, `SELECT app_name, request_id, stage, intent_json, gate_json FROM runtime_activation_checkpoints WHERE app_name = ? AND request_id = ?`, appName, requestID).Scan(&checkpoint.AppName, &checkpoint.RequestID, &checkpoint.Stage, &checkpoint.IntentJSON, &checkpoint.GateJSON)
	if err != nil {
		return checkpoint, mapSQLError(err)
	}
	return checkpoint, nil
}
