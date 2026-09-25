package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
)

type interruptedWithdrawalRuntime struct {
	*submissionRuntime
	fail bool
}

func (r *interruptedWithdrawalRuntime) EnsureCandidateRecoveryNamespace(context.Context) error {
	if r.fail {
		return errors.New("temporary namespace failure")
	}
	return nil
}

func TestMissingSubmissionWithdrawalSurvivesInterruptedRecovery(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "existing"}[existing], func(t *testing.T) {
			service, database, base, req := submissionFixture(t)
			runtime := &interruptedWithdrawalRuntime{submissionRuntime: base, fail: true}
			service.runtime = runtime
			ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
			lookup := &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: req.RequestId}
			if existing {
				base.failDependencies = true
				if _, err := service.DeployApp(ctx, req); err == nil {
					t.Fatal("expected interrupted submission")
				}
				base.failDependencies = false
				if _, err := service.RecoverDeployRequest(ctx, lookup); err == nil {
					t.Fatal("expected interrupted recovery")
				}
			} else if _, err := service.WithdrawDeployRequest(ctx, req); err == nil {
				t.Fatal("expected interrupted withdrawal")
			}
			if marked, err := db.NewCandidateBindingRepository(database).WithdrawalRequested(ctx, lookup.AppName, lookup.RequestId); err != nil || !marked {
				t.Fatalf("withdrawal decision lost: %v %v", marked, err)
			}
			dependencies, prepared := base.dependencies, base.prepareCalls
			if _, err := service.DeployApp(ctx, req); err == nil {
				t.Fatal("late submit allowed to activate")
			}
			if _, err := service.AdvanceDeployRequest(ctx, lookup); err == nil {
				t.Fatal("late advance allowed to activate")
			}
			if base.dependencies != dependencies || base.prepareCalls != prepared {
				t.Fatal("withdrawn operation prepared workloads")
			}
			runtime.fail = false
			result, err := service.WithdrawDeployRequest(ctx, req)
			if err != nil || result.State != "withdrawn" || !result.Result.WithdrawalConfirmed {
				t.Fatalf("withdrawal retry: %+v %v", result, err)
			}
			replay, err := service.DeployApp(ctx, req)
			if err != nil || !replay.WithdrawalConfirmed {
				t.Fatalf("late submit replay: %+v %v", replay, err)
			}
			if base.dependencies != dependencies || base.prepareCalls != prepared {
				t.Fatal("terminal replay prepared workloads")
			}
			if _, err := db.NewAppRepository(database).FindActiveByName(ctx, lookup.AppName); err == nil {
				t.Fatal("withdrawal exposed initial app")
			}
			changed := &deployerv1.DeployAppRequest{RequestId: req.RequestId, ReportWithdrawal: true, DeployerYaml: strings.Replace(req.DeployerYaml, ":1.0.0", ":2.0.0", 1)}
			if _, err := service.WithdrawDeployRequest(ctx, changed); err == nil {
				t.Fatal("changed original configuration accepted")
			}
		})
	}
}

func TestMissingSubmissionWithdrawalPreservesAppliedWinner(t *testing.T) {
	service, database, runtime, req := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	runtime.previousReady = true
	if _, err := service.DeployApp(ctx, req); err != nil {
		t.Fatal(err)
	}
	result, err := service.WithdrawDeployRequest(ctx, req)
	if err != nil || result.State != "applied" {
		t.Fatalf("applied winner: %+v %v", result, err)
	}
	if marked, err := db.NewCandidateBindingRepository(database).WithdrawalRequested(ctx, "hosted-api", req.RequestId); err != nil || marked {
		t.Fatalf("applied winner marked: %v %v", marked, err)
	}
}

func TestMissingSubmissionWithdrawalRequiresAdminAndEnabledRuntime(t *testing.T) {
	service, _, _, req := submissionFixture(t)
	if _, err := service.WithdrawDeployRequest(context.Background(), req); err == nil {
		t.Fatal("anonymous withdrawal allowed")
	}
	service.enableCandidateOperations = false
	if _, err := service.WithdrawDeployRequest(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), req); err == nil {
		t.Fatal("disabled withdrawal allowed")
	}
}
