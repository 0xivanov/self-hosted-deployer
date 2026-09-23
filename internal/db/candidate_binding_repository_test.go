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

func TestCandidateBindingBeginAtomicReplayAndActiveReuse(t *testing.T) {
	database := openRepositoryTestDB(t)
	cfg, _ := candidateBindingConfig(t)
	cfg.EnvironmentRevision = strings.Repeat("1", 64)
	state, err := cfg.JSON()
	if err != nil {
		t.Fatalf("encode candidate state: %v", err)
	}
	now := time.Now().UTC()
	repo := NewCandidateBindingRepository(database)
	id := strings.Repeat("d", 64)
	binding, created, err := repo.Begin(context.Background(), domain.DeployRequest{AppName: cfg.Name, RequestID: id, RequestedState: state, ReportWithdrawal: true}, "app-new", "deployment-new", now)
	if err != nil || !created || binding.AppID != "app-new" {
		t.Fatalf("initial begin: %#v created=%v err=%v", binding, created, err)
	}
	replay, created, err := repo.Begin(context.Background(), domain.DeployRequest{AppName: cfg.Name, RequestID: id, RequestedState: state, ReportWithdrawal: true}, "other-app", "other-deployment", now)
	if err != nil || created || replay.AppID != "app-new" {
		t.Fatalf("replay: %#v created=%v err=%v", replay, created, err)
	}
	if _, _, err := repo.Begin(context.Background(), domain.DeployRequest{AppName: cfg.Name, RequestID: id, RequestedState: state, ReportWithdrawal: false}, "app-new", "deployment-new", now); !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("changed report flag accepted: %v", err)
	}
}

func TestCandidateBindingBeginActiveAndLegacyUnboundRefusal(t *testing.T) {
	database := openRepositoryTestDB(t)
	cfg, _ := candidateBindingConfig(t)
	cfg.EnvironmentRevision = strings.Repeat("2", 64)
	state, _ := cfg.JSON()
	now := time.Now().UTC()
	apps := NewAppRepository(database)
	previous := cfg
	previous.Image = "example/hosted-api:0.9.0"
	previousState, err := previous.JSON()
	if err != nil {
		t.Fatalf("encode predecessor: %v", err)
	}
	if err := apps.Create(context.Background(), domain.App{ID: "active-app", Name: cfg.Name, Image: previous.Image, DesiredStateJSON: previousState, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create active app: %v", err)
	}
	repo := NewCandidateBindingRepository(database)
	requestID := strings.Repeat("e", 64)
	binding, created, err := repo.Begin(context.Background(), domain.DeployRequest{AppName: cfg.Name, RequestID: requestID, RequestedState: state}, "proposed-app", "deployment-active", now)
	if err != nil || !created || binding.AppID != "active-app" {
		t.Fatalf("active begin: %#v created=%v err=%v", binding, created, err)
	}
	legacyID := strings.Repeat("f", 64)
	legacyCfg := cfg
	legacyCfg.Name = "legacy-hosted-api"
	legacyState, _ := legacyCfg.JSON()
	seedCandidateRequest(t, database, legacyCfg, legacyState, legacyID)
	if _, _, err := repo.Begin(context.Background(), domain.DeployRequest{AppName: legacyCfg.Name, RequestID: legacyID, RequestedState: legacyState}, "app", "deployment", now); !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("unbound legacy request adopted: %v", err)
	}
}

func TestCandidateBindingBeginReusesDeletedAppIdentity(t *testing.T) {
	database := openRepositoryTestDB(t)
	ctx := context.Background()
	cfg, state := candidateBindingConfig(t)
	now := time.Now().UTC()
	oldStateCfg := cfg
	oldStateCfg.Image = "example/hosted-api:old"
	oldState, err := oldStateCfg.JSON()
	if err != nil {
		t.Fatal(err)
	}
	apps := NewAppRepository(database)
	if err := apps.Create(ctx, domain.App{ID: "deleted-app", Name: cfg.Name, Image: oldStateCfg.Image, DesiredStateJSON: oldState, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := apps.MarkDeleted(ctx, cfg.Name, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	requestID := strings.Repeat("6", 64)
	binding, created, err := NewCandidateBindingRepository(database).Begin(ctx, domain.DeployRequest{AppName: cfg.Name, RequestID: requestID, RequestedState: state}, "fresh-app-id", "deleted-reuse-deployment", now.Add(2*time.Second))
	if err != nil || !created || binding.AppID != "deleted-app" {
		t.Fatalf("deleted identity not reused: %#v created=%v err=%v", binding, created, err)
	}
	saved, err := apps.FindByName(ctx, cfg.Name)
	if err != nil || saved.ID != "deleted-app" || saved.Image != oldStateCfg.Image || saved.DesiredStateJSON != oldState || saved.DeletedAt == nil {
		t.Fatalf("deleted app mutated: %#v err=%v", saved, err)
	}
}

func TestCandidateBindingBeginAllowsEmptyEnvironmentRevision(t *testing.T) {
	database := openRepositoryTestDB(t)
	cfg, state := candidateBindingConfig(t)
	if cfg.EnvironmentRevision != "" {
		t.Fatal("fixture unexpectedly has an environment revision")
	}
	repo := NewCandidateBindingRepository(database)
	binding, created, err := repo.Begin(context.Background(), domain.DeployRequest{
		AppName: cfg.Name, RequestID: strings.Repeat("1", 64), RequestedState: state,
	}, "empty-env-app", "empty-env-deployment", time.Now().UTC())
	if err != nil || !created || binding.AppID != "empty-env-app" {
		t.Fatalf("empty environment revision rejected: %#v created=%v err=%v", binding, created, err)
	}
}

func TestCandidateBindingBeginRejectsInvalidActivePredecessorAtomically(t *testing.T) {
	database := openRepositoryTestDB(t)
	cfg, _ := candidateBindingConfig(t)
	cfg.EnvironmentRevision = strings.Repeat("2", 64)
	requested, err := cfg.JSON()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := NewAppRepository(database).Create(context.Background(), domain.App{
		ID: "bad-active", Name: cfg.Name, Image: cfg.Image, DesiredStateJSON: `{"name":"bad-active","image":"broken"}`,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err = NewCandidateBindingRepository(database).Begin(context.Background(), domain.DeployRequest{
		AppName: cfg.Name, RequestID: strings.Repeat("2", 64), RequestedState: requested,
	}, "new-app", "new-deployment", now)
	if !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("invalid predecessor accepted: %v", err)
	}
	if _, err := NewDeploymentRequestRepository(database).Find(context.Background(), cfg.Name, strings.Repeat("2", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("request inserted after predecessor rejection: %v", err)
	}
}

func TestCandidateBindingBeginDomainAdmission(t *testing.T) {
	database := openRepositoryTestDB(t)
	cfg, _ := candidateBindingConfig(t)
	cfg.Routing.Domain = "shared.example.test"
	firstState, err := cfg.JSON()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	repo := NewCandidateBindingRepository(database)
	if _, created, err := repo.Begin(context.Background(), domain.DeployRequest{AppName: cfg.Name, RequestID: strings.Repeat("3", 64), RequestedState: firstState}, "first-app", "first-deployment", now); err != nil || !created {
		t.Fatalf("first domain candidate: created=%v err=%v", created, err)
	}
	second := cfg
	second.Name = "other-hosted-api"
	secondState, err := second.JSON()
	if err != nil {
		t.Fatal(err)
	}
	requestID := strings.Repeat("4", 64)
	if _, _, err := repo.Begin(context.Background(), domain.DeployRequest{AppName: second.Name, RequestID: requestID, RequestedState: secondState}, "second-app", "second-deployment", now); !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("domain collision accepted: %v", err)
	}
	if _, err := NewDeploymentRequestRepository(database).Find(context.Background(), second.Name, requestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("domain collision left request: %v", err)
	}
}

func TestCandidateBindingBeginDuplicateDeploymentRollsBackAllRows(t *testing.T) {
	database := openRepositoryTestDB(t)
	ctx := context.Background()
	cfg, state := candidateBindingConfig(t)
	now := time.Now().UTC()
	if err := NewAppRepository(database).Create(ctx, domain.App{ID: "collision-owner", Name: "collision-owner", Image: cfg.Image, DesiredStateJSON: state, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := NewDeploymentRepository(database).Create(ctx, domain.Deployment{ID: "collision-deployment", AppID: "collision-owner", Status: "pending", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("5", 64)
	if _, _, err := NewCandidateBindingRepository(database).Begin(ctx, domain.DeployRequest{AppName: cfg.Name, RequestID: id, RequestedState: state}, "rollback-app", "collision-deployment", now); err == nil {
		t.Fatal("duplicate deployment unexpectedly succeeded")
	}
	if _, err := NewDeploymentRequestRepository(database).Find(ctx, cfg.Name, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("request survived rollback: %v", err)
	}
	if _, err := NewAppRepository(database).FindByName(ctx, cfg.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app survived rollback: %v", err)
	}
	if _, err := NewCandidateBindingRepository(database).Find(ctx, cfg.Name, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("binding survived rollback: %v", err)
	}
}

func TestCandidateBindingBeginRejectsAnotherPendingRequestAtomically(t *testing.T) {
	database := openRepositoryTestDB(t)
	ctx := context.Background()
	cfg, state := candidateBindingConfig(t)
	now := time.Now().UTC()
	firstID := strings.Repeat("7", 64)
	if _, created, err := NewDeploymentRequestRepository(database).Begin(ctx, domain.DeployRequest{
		AppName: cfg.Name, RequestID: firstID, RequestedState: state, CreatedAt: now, UpdatedAt: now,
	}); err != nil || !created {
		t.Fatalf("seed pending request: created=%v err=%v", created, err)
	}
	secondID := strings.Repeat("8", 64)
	if _, _, err := NewCandidateBindingRepository(database).Begin(ctx, domain.DeployRequest{
		AppName: cfg.Name, RequestID: secondID, RequestedState: state,
	}, "pending-conflict-app", "pending-conflict-deployment", now); !errors.Is(err, ErrCandidateBindingConflict) {
		t.Fatalf("second pending request accepted: %v", err)
	}
	if _, err := NewAppRepository(database).FindByName(ctx, cfg.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending conflict created app: %v", err)
	}
	if _, err := NewDeploymentRepository(database).FindByID(ctx, "pending-conflict-deployment"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending conflict created deployment: %v", err)
	}
	if _, err := NewCandidateBindingRepository(database).Find(ctx, cfg.Name, secondID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending conflict created binding: %v", err)
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
