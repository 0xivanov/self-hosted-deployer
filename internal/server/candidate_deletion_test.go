package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
)

type deletionRuntime struct {
	*submissionRuntime
	deleting, clean, failFinish bool
	beginCalls, finishCalls     int
	allowMissing                bool
	retiredRequests             []string
	beforeFinish                func()
}

func (r *deletionRuntime) CheckCandidateDeletion(context.Context, string) error {
	if r.deleting {
		return errors.New("deletion pending")
	}
	return nil
}
func (r *deletionRuntime) BeginCandidateDeletion(_ context.Context, _ appconfig.Config, _ string, allowMissing bool) (ingress.ActivationGate, error) {
	r.beginCalls++
	r.allowMissing = allowMissing
	r.deleting = true
	return r.gate, nil
}
func (r *deletionRuntime) RetireCandidateDeployment(ctx context.Context, cfg appconfig.Config, revision, request, registry string) error {
	r.retiredRequests = append(r.retiredRequests, request)
	return r.retirementRuntime.RetireCandidateDeployment(ctx, cfg, revision, request, registry)
}
func (r *deletionRuntime) CleanupCandidateDeletion(context.Context, ingress.ActivationGate, string) (bool, error) {
	return r.clean, nil
}
func (r *deletionRuntime) FinishCandidateDeletion(context.Context, ingress.ActivationGate, string) error {
	r.finishCalls++
	if r.beforeFinish != nil {
		r.beforeFinish()
	}
	if r.failFinish {
		return errors.New("service deletion reply unavailable")
	}
	r.deleting = false
	return nil
}

func TestCandidateDeletionDrainsAllReleasesAndBlocksSubmission(t *testing.T) {
	service, database, submission, req := submissionFixture(t)
	runtime := &deletionRuntime{submissionRuntime: submission}
	service.runtime = runtime
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	submission.previousReady = true
	first, err := service.DeployApp(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second := &deployerv1.DeployAppRequest{RequestId: strings.Repeat("b", 64), ReportWithdrawal: true, DeployerYaml: strings.Replace(req.DeployerYaml, ":1.0.0", ":2.0.0", 1)}
	if _, err = service.DeployApp(ctx, second); err != nil {
		t.Fatal(err)
	}
	deleteRequest := &deployerv1.DeleteAppRequest{Name: "hosted-api"}
	if _, err = service.DeleteApp(ctx, deleteRequest); err == nil {
		t.Fatal("deletion finished before runtime cleanup")
	}
	if len(runtime.retiredRequests) != 2 || runtime.retiredRequests[0] != req.RequestId || runtime.retiredRequests[1] != second.RequestId {
		t.Fatalf("not all releases retired: %v", runtime.retiredRequests)
	}
	if _, err = service.apps.FindActiveByName(ctx, "hosted-api"); err != nil {
		t.Fatal("app hidden before runtime cleanup")
	}
	third := &deployerv1.DeployAppRequest{RequestId: strings.Repeat("c", 64), ReportWithdrawal: true, DeployerYaml: req.DeployerYaml}
	if _, err = service.DeployApp(ctx, third); err == nil {
		t.Fatal("new request admitted during deletion")
	}
	if _, err = db.NewDeploymentRequestRepository(database).Find(ctx, "hosted-api", third.RequestId); !errors.Is(err, db.ErrNotFound) {
		t.Fatal("blocked request was journaled")
	}
	if _, err = service.DeployApp(ctx, &deployerv1.DeployAppRequest{DeployerYaml: req.DeployerYaml}); err == nil {
		t.Fatal("legacy submission bypassed deletion barrier")
	}
	if _, err = service.PreflightApp(ctx, &deployerv1.PreflightAppRequest{DeployerYaml: req.DeployerYaml}); err == nil {
		t.Fatal("preflight reported readiness during deletion")
	}
	runtime.clean = true
	runtime.beforeFinish = func() {
		app, lookupErr := service.apps.FindByID(ctx, first.App.Id)
		if lookupErr != nil || app.DeletedAt == nil {
			t.Fatal("Service removed before app marked deleted")
		}
	}
	result, err := service.DeleteApp(ctx, deleteRequest)
	if err != nil || result.App.Id != first.App.Id {
		t.Fatalf("delete: %+v %v", result, err)
	}
	if _, err = service.apps.FindActiveByName(ctx, "hosted-api"); !errors.Is(err, db.ErrNotFound) {
		t.Fatal("deleted app still active")
	}
	if routes, err := service.routes.ListByApp(ctx, first.App.Id); err != nil || len(routes) != 0 {
		t.Fatal("deleted app retained routes")
	}
}

func TestCandidateDeletionResumesAfterDatabaseMarkAndHandlesHiddenApp(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "withdrawn initial"}[hidden], func(t *testing.T) {
			service, _, submission, req := submissionFixture(t)
			runtime := &deletionRuntime{submissionRuntime: submission, clean: true, failFinish: true}
			service.runtime = runtime
			ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
			submission.previousReady = !hidden
			first, err := service.DeployApp(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if hidden {
				if _, err = service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: req.RequestId}); err != nil {
					t.Fatal(err)
				}
			}
			deleteRequest := &deployerv1.DeleteAppRequest{Name: "hosted-api"}
			if _, err = service.DeleteApp(ctx, deleteRequest); err == nil {
				t.Fatal("lost final reply incorrectly reported success")
			}
			app, err := service.apps.FindByID(ctx, first.App.Id)
			if err != nil || app.DeletedAt == nil || !runtime.deleting {
				t.Fatal("durable barrier not retained after database mark")
			}
			runtime.failFinish = false
			result, err := service.DeleteApp(ctx, deleteRequest)
			if err != nil || result.App.Id != first.App.Id || !runtime.allowMissing || runtime.finishCalls != 2 {
				t.Fatalf("resume hidden deletion: %+v %v", result, err)
			}
		})
	}
}

func TestCandidateDeletionRejectsPendingDeploymentBeforeRuntimeWrites(t *testing.T) {
	service, _, submission, req := submissionFixture(t)
	runtime := &deletionRuntime{submissionRuntime: submission, clean: true}
	service.runtime = runtime
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	if _, err := service.DeployApp(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "hosted-api"}); err == nil || runtime.beginCalls != 0 {
		t.Fatal("pending request allowed deletion")
	}
}
