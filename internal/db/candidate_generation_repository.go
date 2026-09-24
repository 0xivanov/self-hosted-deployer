package db

import (
	"context"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

// GenerationsByApp includes withdrawn generations as well as applied ones:
// both can leave runtime resources that must be retired during app deletion.
func (r *CandidateBindingRepository) GenerationsByApp(ctx context.Context, appName, appID string) ([]domain.CandidateGeneration, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT b.app_name, b.app_id, b.request_id, r.requested_state, r.state
		FROM candidate_request_bindings b JOIN deployment_requests r
		ON r.app_name = b.app_name AND r.request_id = b.request_id
		WHERE b.app_name = ? AND b.app_id = ? ORDER BY b.created_at, b.request_id`, appName, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.CandidateGeneration
	for rows.Next() {
		var generation domain.CandidateGeneration
		if err := rows.Scan(&generation.AppName, &generation.AppID, &generation.RequestID, &generation.RequestedState, &generation.State); err != nil {
			return nil, err
		}
		result = append(result, generation)
	}
	return result, rows.Err()
}
