package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
)

func finalizationFixture(t *testing.T, previous bool, outcome string) (*Db, string, string) {
	t.Helper()
	database := openRepositoryTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	id := strings.Repeat("a", 64)
	cfg, err := appconfig.Parse([]byte(`name: hosted-api
image: example/api:v2
service:
  port: 8080
  health: {path: /health}
routing:
  domain: site.example.com
deploy:
  replicas: 1
hosting:
  version: v1
  maxReplicas: 2
  resources:
    requests: {cpu: 100m, memory: 128Mi, ephemeralStorage: 1Gi}
    limits: {cpu: 500m, memory: 512Mi, ephemeralStorage: 2Gi}
`))
	if err != nil {
		t.Fatal(err)
	}
	requested, err := cfg.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if previous {
		old := cfg
		old.Image = "example/api:v1"
		oldState, _ := old.JSON()
		if err = NewAppRepository(database).Create(ctx, domain.App{ID: "app-1", Name: cfg.Name, Image: old.Image, DesiredStateJSON: oldState, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err = NewRouteRepository(database).Create(ctx, domain.Route{ID: "route-old", AppID: "app-1", Domain: cfg.Routing.Domain, TargetPort: 8080, Status: "ready", TLSEnabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = NewDeploymentRequestRepository(database).Begin(ctx, domain.DeployRequest{AppName: cfg.Name, RequestID: id, RequestedState: requested, ReportWithdrawal: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = NewCandidateBindingRepository(database).Bind(ctx, cfg.Name, id, "app-1", "deployment-1", now); err != nil {
		t.Fatal(err)
	}
	repo := NewRuntimeCheckpointRepository(database)
	intent, _ := json.Marshal(map[string]string{"app_name": cfg.Name, "request_id": id, "requested_state": requested})
	if err = repo.SaveActivationIntent(ctx, cfg.Name, id, string(intent)); err != nil {
		t.Fatal(err)
	}
	if outcome == "applied" {
		err = repo.RecordActivationGate(ctx, cfg.Name, id, "prepared", "fenced", `{"gate":"fenced"}`)
		if err == nil {
			err = repo.RecordActivationGate(ctx, cfg.Name, id, "fenced", "activated", `{"gate":"final"}`)
		}
	} else {
		err = repo.RecordActivationGate(ctx, cfg.Name, id, "prepared", "recovering", `{"gate":"recovering"}`)
		if err == nil {
			err = repo.RecordActivationGate(ctx, cfg.Name, id, "recovering", "withdrawn", `{"gate":"final"}`)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	return database, id, requested
}

func TestCandidateFinalizationAppliedAndReplay(t *testing.T) {
	for _, previous := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "update"}[previous], func(t *testing.T) {
			database, id, requested := finalizationFixture(t, previous, "applied")
			ctx := context.Background()
			repo := NewCandidateFinalizationRepository(database)
			result, err := repo.Finalize(ctx, "hosted-api", id, "applied", `{"gate":"final"}`, true, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var reply deployerv1.DeployAppResponse
			if err = json.Unmarshal([]byte(result), &reply); err != nil {
				t.Fatal(err)
			}
			if reply.App.Id != "app-1" || reply.App.DesiredState != requested || reply.Deployment.Id != "deployment-1" || reply.Deployment.Status != "healthy" || reply.WithdrawalConfirmed {
				t.Fatalf("bad receipt: %s", result)
			}
			app, err := NewAppRepository(database).FindActiveByName(ctx, "hosted-api")
			if err != nil || app.DesiredStateJSON != requested {
				t.Fatalf("app: %+v %v", app, err)
			}
			route, err := NewRouteRepository(database).FindByDomain(ctx, "site.example.com")
			if err != nil || route.AppID != app.ID || !route.TLSEnabled || route.Status != "pending" {
				t.Fatalf("route: %+v %v", route, err)
			}
			replay, err := repo.Finalize(ctx, "hosted-api", id, "applied", `{"gate":"final"}`, true, time.Now().Add(time.Hour))
			if err != nil || replay != result {
				t.Fatalf("replay changed: %v", err)
			}
			if _, err = repo.Finalize(ctx, "hosted-api", id, "withdrawn", `{"gate":"final"}`, true, time.Now()); !errors.Is(err, ErrCandidateFinalizationConflict) {
				t.Fatal("terminal outcome changed")
			}
		})
	}
}

func TestCandidateFinalizationWithdrawalPreservesPredecessor(t *testing.T) {
	for _, previous := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "update"}[previous], func(t *testing.T) {
			database, id, _ := finalizationFixture(t, previous, "withdrawn")
			ctx := context.Background()
			before, _ := NewAppRepository(database).FindByName(ctx, "hosted-api")
			result, err := NewCandidateFinalizationRepository(database).Finalize(ctx, "hosted-api", id, "withdrawn", `{"gate":"final"}`, true, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			after, _ := NewAppRepository(database).FindByName(ctx, "hosted-api")
			if after.DesiredStateJSON != before.DesiredStateJSON || !after.UpdatedAt.Equal(before.UpdatedAt) || (after.DeletedAt != nil) != (!previous) {
				t.Fatalf("predecessor changed: %+v", after)
			}
			var reply deployerv1.DeployAppResponse
			_ = json.Unmarshal([]byte(result), &reply)
			if !reply.WithdrawalConfirmed || reply.Deployment.Status != "failed" {
				t.Fatal(result)
			}
			routes, err := NewRouteRepository(database).ListByApp(ctx, "app-1")
			if err != nil {
				t.Fatal(err)
			}
			if previous && (len(routes) != 1 || routes[0].ID != "route-old" || routes[0].Status != "ready") {
				t.Fatal("predecessor route changed")
			}
			if !previous && len(routes) != 0 {
				t.Fatal("initial route survived")
			}
		})
	}
}

func TestCandidateFinalizationRollsBackAllWrites(t *testing.T) {
	database, id, _ := finalizationFixture(t, true, "applied")
	ctx := context.Background()
	before, _ := NewAppRepository(database).FindByName(ctx, "hosted-api")
	// Fail the final journal write, after app, route and deployment mutations.
	if _, err := database.conn.Exec(`CREATE TRIGGER reject_candidate_completion BEFORE UPDATE ON deployment_requests BEGIN SELECT RAISE(ABORT, 'injected final write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCandidateFinalizationRepository(database).Finalize(ctx, "hosted-api", id, "applied", `{"gate":"final"}`, true, time.Now()); err == nil {
		t.Fatal("expected failure")
	}
	after, _ := NewAppRepository(database).FindByName(ctx, "hosted-api")
	deployment, _ := NewDeploymentRepository(database).FindByID(ctx, "deployment-1")
	request, _ := NewDeploymentRequestRepository(database).Find(ctx, "hosted-api", id)
	route, _ := NewRouteRepository(database).FindByDomain(ctx, "site.example.com")
	if after.DesiredStateJSON != before.DesiredStateJSON || deployment.Status != "pending" || request.State != "pending" || request.ResponseJSON != "" || route.Status != "ready" {
		t.Fatal("partial completion escaped transaction")
	}
}

func TestCandidateFinalizationRejectsStaleEvidence(t *testing.T) {
	database, id, _ := finalizationFixture(t, true, "applied")
	ctx := context.Background()
	repo := NewCandidateFinalizationRepository(database)
	for _, input := range []struct{ outcome, gate string }{{"withdrawn", `{"gate":"final"}`}, {"applied", `{"gate":"stale"}`}} {
		if _, err := repo.Finalize(ctx, "hosted-api", id, input.outcome, input.gate, true, time.Now()); !errors.Is(err, ErrCandidateFinalizationConflict) {
			t.Fatalf("accepted stale evidence: %v", err)
		}
	}
	if _, err := database.conn.Exec(`UPDATE apps SET desired_state_json='{}' WHERE id='app-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Finalize(ctx, "hosted-api", id, "applied", `{"gate":"final"}`, true, time.Now()); !errors.Is(err, ErrCandidateFinalizationConflict) {
		t.Fatalf("accepted changed predecessor: %v", err)
	}
}

func TestBoundCandidateCannotUseLegacyRequestCompletion(t *testing.T) {
	database, id, _ := finalizationFixture(t, true, "applied")
	ctx := context.Background()
	if err := NewDeploymentRequestRepository(database).Complete(ctx, "hosted-api", id, "applied", `{"app":{}}`, time.Now()); err == nil {
		t.Fatal("legacy completion bypassed atomic finalizer")
	}
	request, err := NewDeploymentRequestRepository(database).Find(ctx, "hosted-api", id)
	if err != nil || request.State != "pending" {
		t.Fatalf("bound request changed: %+v %v", request, err)
	}
}
