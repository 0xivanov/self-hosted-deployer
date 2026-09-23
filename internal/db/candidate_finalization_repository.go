package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
)

var ErrCandidateFinalizationConflict = errors.New("candidate finalization conflicts with saved operation")

type CandidateFinalizationRepository struct{ db *Db }

func NewCandidateFinalizationRepository(db *Db) *CandidateFinalizationRepository {
	return &CandidateFinalizationRepository{db: db}
}

// Finalize commits the candidate's app, deployment, route and immutable reply
// together. The caller must hold the app mutation lock and verify runtime
// activation or full recovery (including retirement) before calling. A withdrawn
// checkpoint proves routing restoration only; this method does not prove runtime
// health. expectedGate is the exact checkpoint inspected by that caller.
func (r *CandidateFinalizationRepository) Finalize(ctx context.Context, appName, requestID, outcome, expectedGate string, tlsEnabled bool, now time.Time) (string, error) {
	if appName == "" || !deployRequestIDPattern.MatchString(requestID) || (outcome != "applied" && outcome != "withdrawn") || expectedGate == "" || now.IsZero() {
		return "", ErrCandidateFinalizationConflict
	}
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var state, requested, appID, deploymentID string
	var previousID, previousState, response sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT r.state,r.requested_state,r.previous_app_id,r.previous_state,r.response_json,b.app_id,b.deployment_id FROM deployment_requests r JOIN candidate_request_bindings b ON b.app_name=r.app_name AND b.request_id=r.request_id WHERE r.app_name=? AND r.request_id=?`, appName, requestID).Scan(&state, &requested, &previousID, &previousState, &response, &appID, &deploymentID)
	if err != nil {
		return "", mapSQLError(err)
	}
	if state != "pending" {
		if state == outcome && response.Valid && json.Valid([]byte(response.String)) {
			return response.String, nil
		}
		return "", ErrCandidateFinalizationConflict
	}
	var stage, gate, intentJSON string
	if err = tx.QueryRowContext(ctx, `SELECT stage,gate_json,intent_json FROM runtime_activation_checkpoints WHERE app_name=? AND request_id=?`, appName, requestID).Scan(&stage, &gate, &intentJSON); err != nil {
		return "", mapSQLError(err)
	}
	wantedStage := "activated"
	if outcome == "withdrawn" {
		wantedStage = "withdrawn"
	}
	if stage != wantedStage || gate != expectedGate {
		return "", ErrCandidateFinalizationConflict
	}
	var intent struct {
		AppName        string `json:"app_name"`
		RequestID      string `json:"request_id"`
		RequestedState string `json:"requested_state"`
	}
	if json.Unmarshal([]byte(intentJSON), &intent) != nil || intent.AppName != appName || intent.RequestID != requestID || intent.RequestedState != requested {
		return "", ErrCandidateFinalizationConflict
	}
	cfg, err := appconfig.FromJSON(requested)
	if err != nil {
		return "", err
	}
	if err = cfg.Validate(); err != nil {
		return "", err
	}
	if cfg.Name != appName || cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode {
		return "", ErrCandidateFinalizationConflict
	}
	app, err := scanApp(tx.QueryRowContext(ctx, `SELECT id,name,image,desired_state_json,created_at,updated_at,deleted_at FROM apps WHERE id=?`, appID))
	if err != nil {
		return "", err
	}
	if app.Name != appName || (previousID.String == "") != (previousState.String == "") {
		return "", ErrCandidateFinalizationConflict
	}
	if previousID.String != "" {
		if app.ID != previousID.String || app.DesiredStateJSON != previousState.String || app.DeletedAt != nil {
			return "", ErrCandidateFinalizationConflict
		}
	} else if app.DeletedAt == nil {
		return "", ErrCandidateFinalizationConflict
	}
	deployment, err := scanDeployment(tx.QueryRowContext(ctx, `SELECT id,app_id,status,failure_reason,created_at,updated_at FROM deployments WHERE id=?`, deploymentID))
	if err != nil {
		return "", err
	}
	if deployment.AppID != appID || deployment.Status != "pending" {
		return "", ErrCandidateFinalizationConflict
	}
	if outcome == "applied" {
		app.Image = cfg.Image
		app.DesiredStateJSON = requested
		app.UpdatedAt = now
		app.DeletedAt = nil
		if err = mapRowsAffected(tx.ExecContext(ctx, `UPDATE apps SET image=?,desired_state_json=?,updated_at=?,deleted_at=NULL WHERE id=?`, app.Image, requested, formatTime(now), appID)); err != nil {
			return "", err
		}
		if cfg.Routing.Domain == "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM routes WHERE app_id=?`, appID)
		} else {
			sum := sha256.Sum256([]byte(appName + "\x00" + requestID))
			routeID := "route_" + hex.EncodeToString(sum[:16])
			_, err = tx.ExecContext(ctx, `INSERT INTO routes (id,app_id,domain,target_port,status,tls_enabled,created_at,updated_at) VALUES (?,?,?,?,'pending',?,?,?) ON CONFLICT(app_id) DO UPDATE SET domain=excluded.domain,target_port=excluded.target_port,status=excluded.status,tls_enabled=excluded.tls_enabled,updated_at=excluded.updated_at`, routeID, appID, cfg.Routing.Domain, cfg.Service.Port, boolToInt(tlsEnabled), formatTime(now), formatTime(now))
		}
		if err != nil {
			return "", err
		}
		deployment.Status = "healthy"
		deployment.FailureReason = ""
	} else {
		// Binding preserved the predecessor app and routes. For an initial attempt,
		// leave the provisional/deleted app invisible and remove its stale route.
		if previousID.String == "" {
			if _, err = tx.ExecContext(ctx, `DELETE FROM routes WHERE app_id=?`, appID); err != nil {
				return "", err
			}
		}
		deployment.Status = "failed"
		deployment.FailureReason = "Deployment withdrawn after confirmed runtime recovery"
	}
	deployment.UpdatedAt = now
	if err = mapRowsAffected(tx.ExecContext(ctx, `UPDATE deployments SET status=?,failure_reason=?,updated_at=? WHERE id=? AND status='pending'`, deployment.Status, optionalString(deployment.FailureReason), formatTime(now), deploymentID)); err != nil {
		return "", err
	}
	resultCfg, err := appconfig.FromJSON(app.DesiredStateJSON)
	if err != nil {
		return "", err
	}
	reply := &deployerv1.DeployAppResponse{
		App:            &deployerv1.App{Id: app.ID, Name: app.Name, Image: app.Image, Replicas: int32(resultCfg.Deploy.Replicas), DesiredState: app.DesiredStateJSON, CreatedAt: app.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: app.UpdatedAt.UTC().Format(time.RFC3339Nano)},
		Deployment:     &deployerv1.Deployment{Id: deployment.ID, AppId: appID, Status: deployment.Status, FailureReason: deployment.FailureReason, CreatedAt: deployment.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: now.UTC().Format(time.RFC3339Nano)},
		RequestedState: requested, WithdrawalConfirmed: outcome == "withdrawn",
	}
	encoded, err := json.Marshal(reply)
	if err != nil {
		return "", err
	}
	if err = mapRowsAffected(tx.ExecContext(ctx, `UPDATE deployment_requests SET state=?,response_json=?,updated_at=? WHERE app_name=? AND request_id=? AND state='pending'`, outcome, string(encoded), formatTime(now), appName, requestID)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return string(encoded), nil
}
