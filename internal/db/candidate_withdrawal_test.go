package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestCandidateWithdrawalBindingIsAtomic(t *testing.T) {
	database := openRepositoryTestDB(t)
	cfg, state := candidateBindingConfig(t)
	ctx := context.Background()
	repo := NewCandidateBindingRepository(database)
	now := time.Now().UTC()
	req := domain.DeployRequest{AppName: cfg.Name, RequestID: strings.Repeat("a", 64), RequestedState: state, State: "pending", ReportWithdrawal: true, CreatedAt: now, UpdatedAt: now}
	if _, err := database.conn.ExecContext(ctx, `CREATE TRIGGER reject_withdrawal BEFORE INSERT ON deployment_request_withdrawals BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BeginWithdrawal(ctx, req, "app-1", "deploy-1", now); err == nil {
		t.Fatal("expected marker failure")
	}
	for _, table := range []string{"apps", "deployments", "deployment_requests", "candidate_request_bindings", "deployment_request_withdrawals"} {
		var count int
		if err := database.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s: %d %v", table, count, err)
		}
	}
	if _, err := database.conn.ExecContext(ctx, `DROP TRIGGER reject_withdrawal`); err != nil {
		t.Fatal(err)
	}
	first, created, err := repo.BeginWithdrawal(ctx, req, "app-1", "deploy-1", now)
	if err != nil || !created {
		t.Fatalf("withdrawal: %+v %v %v", first, created, err)
	}
	replay, created, err := repo.Begin(ctx, req, "other-app", "other-deploy", now)
	if err != nil || created || replay.AppID != first.AppID || replay.DeploymentID != first.DeploymentID {
		t.Fatalf("late submission rebound: %+v %v %v", replay, created, err)
	}
	if marked, err := repo.WithdrawalRequested(ctx, req.AppName, req.RequestID); err != nil || !marked {
		t.Fatalf("late submission cleared marker: %v %v", marked, err)
	}
	req.ReportWithdrawal = false
	if _, _, err := repo.BeginWithdrawal(ctx, req, "app-1", "deploy-1", now); err == nil {
		t.Fatal("changed original request accepted")
	}
}
