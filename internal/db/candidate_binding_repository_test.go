package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func candidateBindingConfig(t *testing.T) (appconfig.Config, string) {
	t.Helper()
	cfg, err := appconfig.Parse([]byte(candidateBindingYAML))
	if err != nil {
		t.Fatalf("parse candidate config: %v", err)
	}
	state, err := cfg.JSON()
	if err != nil {
		t.Fatalf("encode candidate config: %v", err)
	}
	return cfg, state
}

func seedCandidateRequest(t *testing.T, database *Db, cfg appconfig.Config, state, id string) {
	t.Helper()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	if _, created, err := NewDeploymentRequestRepository(database).Begin(context.Background(), domain.DeployRequest{AppName: cfg.Name, RequestID: id, RequestedState: state, State: "pending", CreatedAt: now, UpdatedAt: now}); err != nil || !created {
		t.Fatalf("seed deployment request: created=%v err=%v", created, err)
	}
}

func TestCandidateBindingInitialCreateAndReplay(t *testing.T) {
	database := openRepositoryTestDB(t)
	cfg, state := candidateBindingConfig(t)
	id := strings.Repeat("a", 64)
	seedCandidateRequest(t, database, cfg, state, id)
	repo := NewCandidateBindingRepository(database)
	now := time.Now().UTC()
	binding, err := repo.Bind(context.Background(), cfg.Name, id, "app-1", "deployment-1", now)
	if err != nil {
		t.Fatalf("bind candidate: %v", err)
	}
	if binding.AppID != "app-1" || binding.DeploymentID != "deployment-1" {
		t.Fatalf("unexpected binding: %#v", binding)
	}
	replayed, err := repo.Bind(context.Background(), cfg.Name, id, "app-1", "deployment-1", now)
	if err != nil || replayed.AppID != binding.AppID {
		t.Fatalf("replay: %#v err=%v", replayed, err)
	}
	if _, err := database.conn.ExecContext(context.Background(), `UPDATE deployment_requests SET state = 'applied', response_json = '{}' WHERE app_name = ? AND request_id = ?`, cfg.Name, id); err != nil {
		t.Fatalf("terminalize request: %v", err)
	}
	if _, err := repo.Bind(context.Background(), cfg.Name, id, "app-1", "deployment-1", now); err != nil {
		t.Fatalf("terminal binding replay: %v", err)
	}
	if _, err := repo.Bind(context.Background(), cfg.Name, id, "other-app", "deployment-1", now); !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("changed app ID accepted: %v", err)
	}
	app, err := NewAppRepository(database).FindByName(context.Background(), cfg.Name)
	if err != nil || app.ID != "app-1" || app.DeletedAt == nil {
		t.Fatalf("initial app row: %#v err=%v", app, err)
	}
	deployment, err := NewDeploymentRepository(database).FindByID(context.Background(), "deployment-1")
	if err != nil || deployment.Status != "pending" {
		t.Fatalf("pending deployment: %#v err=%v", deployment, err)
	}
}

func TestCandidateBindingRejectsChangedActivePredecessor(t *testing.T) {
	database := openRepositoryTestDB(t)
	cfg, state := candidateBindingConfig(t)
	now := time.Now().UTC()
	apps := NewAppRepository(database)
	if err := apps.Create(context.Background(), domain.App{ID: "app-1", Name: cfg.Name, Image: cfg.Image, DesiredStateJSON: state, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create predecessor: %v", err)
	}
	id := strings.Repeat("b", 64)
	seedCandidateRequest(t, database, cfg, state, id)
	changed := domain.App{ID: "app-1", Name: cfg.Name, Image: "changed:1", DesiredStateJSON: `{"name":"hosted-api","image":"changed:1"}`, CreatedAt: now, UpdatedAt: now}
	if err := apps.Update(context.Background(), changed); err != nil {
		t.Fatalf("change predecessor: %v", err)
	}
	if _, err := NewCandidateBindingRepository(database).Bind(context.Background(), cfg.Name, id, "app-1", "deployment-1", now); !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("changed predecessor accepted: %v", err)
	}
}

const candidateBindingYAML = `
name: hosted-api
image: example/hosted-api:1.0.0
service:
  port: 8080
  health:
    path: /health
routing: {}
deploy:
  replicas: 2
placement: {}
hosting:
  version: v1
  maxReplicas: 3
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
      ephemeralStorage: 1Gi
    limits:
      cpu: 500m
      memory: 512Mi
      ephemeralStorage: 2Gi
`

func TestCandidateBindingReusesDeletedAppWithoutChangingIt(t *testing.T) {
	database := openRepositoryTestDB(t)
	ctx := context.Background()
	cfg, state := candidateBindingConfig(t)
	now := time.Now().UTC()
	old := cfg
	old.Image = "example/api:old"
	oldState, _ := old.JSON()
	app := domain.App{ID: "old-app", Name: cfg.Name, Image: old.Image, DesiredStateJSON: oldState, CreatedAt: now, UpdatedAt: now, DeletedAt: &now}
	if err := NewAppRepository(database).Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("c", 64)
	seedCandidateRequest(t, database, cfg, state, id)
	repo := NewCandidateBindingRepository(database)
	if _, err := repo.Bind(ctx, cfg.Name, id, "new-app", "dep", now); !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("changed app accepted: %v", err)
	}
	if _, err := repo.Bind(ctx, cfg.Name, id, "old-app", "dep", now); err != nil {
		t.Fatal(err)
	}
	saved, err := NewAppRepository(database).FindByName(ctx, cfg.Name)
	if err != nil || saved.DesiredStateJSON != oldState || saved.DeletedAt == nil {
		t.Fatalf("deleted app changed: %+v %v", saved, err)
	}
	if _, err := repo.Bind(ctx, cfg.Name, id, "old-app", "different-dep", now); !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("changed deployment accepted: %v", err)
	}
}

func TestCandidateBindingFailureLeavesNoProvisionalRows(t *testing.T) {
	database := openRepositoryTestDB(t)
	ctx := context.Background()
	cfg, state := candidateBindingConfig(t)
	id := strings.Repeat("d", 64)
	seedCandidateRequest(t, database, cfg, state, id)
	if _, err := database.conn.Exec(`CREATE TRIGGER reject_binding BEFORE INSERT ON candidate_request_bindings BEGIN SELECT RAISE(ABORT, 'injected binding failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCandidateBindingRepository(database).Bind(ctx, cfg.Name, id, "app", "dep", time.Now()); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := NewAppRepository(database).FindByName(ctx, cfg.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("provisional app survived: %v", err)
	}
	if _, err := NewDeploymentRepository(database).FindByID(ctx, "dep"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deployment survived: %v", err)
	}
}
