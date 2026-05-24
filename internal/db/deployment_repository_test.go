package db

import (
	"context"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestDeploymentRepositoryCreateUpdateAndListByApp(t *testing.T) {
	ctx := context.Background()
	repo := NewDeploymentRepository(openRepositoryTestDB(t))
	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	first := domain.Deployment{
		ID:        "deploy-1",
		AppID:     "app-1",
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	}
	second := domain.Deployment{
		ID:        "deploy-2",
		AppID:     "app-1",
		Status:    "pending",
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
	}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("create first deployment: %v", err)
	}
	if err := repo.Create(ctx, second); err != nil {
		t.Fatalf("create second deployment: %v", err)
	}

	if err := repo.UpdateStatus(ctx, "deploy-1", "failed", "image pull failed", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("update deployment status: %v", err)
	}
	updated, err := repo.FindByID(ctx, "deploy-1")
	if err != nil {
		t.Fatalf("find updated deployment: %v", err)
	}
	if updated.Status != "failed" || updated.FailureReason != "image pull failed" {
		t.Fatalf("status update was not stored: %#v", updated)
	}

	deployments, err := repo.ListByApp(ctx, "app-1")
	if err != nil {
		t.Fatalf("list deployments by app: %v", err)
	}
	if len(deployments) != 2 || deployments[0].ID != "deploy-2" || deployments[1].ID != "deploy-1" {
		t.Fatalf("unexpected deployment list: %#v", deployments)
	}
}
