package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestDeploymentRequestBeginCapturesPredecessorAndPreservesItOnRetry(t *testing.T) {
	database := openRepositoryTestDB(t)
	apps := NewAppRepository(database)
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	if err := apps.Create(context.Background(), domain.App{
		ID: "app-1", Name: "my-api", DesiredStateJSON: `{"name":"my-api","image":"old"}`,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create app: %v", err)
	}
	repo := NewDeploymentRequestRepository(database)
	request := domain.DeployRequest{AppName: "my-api", RequestID: strings.Repeat("a", 64), State: "pending", RequestedState: `{"name":"my-api","image":"new"}`, CreatedAt: now, UpdatedAt: now}
	first, created, err := repo.Begin(context.Background(), request)
	if err != nil || !created {
		t.Fatalf("begin: created=%v err=%v", created, err)
	}
	if first.PreviousAppID != "app-1" || first.PreviousState != `{"name":"my-api","image":"old"}` {
		t.Fatalf("predecessor not captured: %#v", first)
	}
	request.PreviousAppID = "caller-controlled"
	request.PreviousState = "caller-controlled"
	retried, created, err := repo.Begin(context.Background(), request)
	if err != nil || created {
		t.Fatalf("retry: created=%v err=%v", created, err)
	}
	if retried.PreviousAppID != "app-1" || retried.PreviousState != `{"name":"my-api","image":"old"}` {
		t.Fatalf("retry replaced predecessor: %#v", retried)
	}
}
