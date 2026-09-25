package server

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
)

type capacityRuntime struct {
	*submissionRuntime
	legacyGone  bool
	retired     []string
	legacyCalls int
}

func (r *capacityRuntime) CheckCandidateDeletion(context.Context, string) error { return nil }
func (r *capacityRuntime) RetireLegacyCandidatePredecessor(context.Context, ingress.ActivationGate) (bool, error) {
	r.legacyCalls++
	return r.legacyGone, nil
}
func (r *capacityRuntime) RetireCandidateDeployment(ctx context.Context, cfg appconfig.Config, revision, request, registry string) error {
	r.retired = append(r.retired, request)
	return r.retirementRuntime.RetireCandidateDeployment(ctx, cfg, revision, request, registry)
}

func TestAppliedCandidateCleanupRetriesWithoutRetiringSelectedRelease(t *testing.T) {
	service, _, submission, first := submissionFixture(t)
	runtime := &capacityRuntime{submissionRuntime: submission, legacyGone: true}
	service.runtime = runtime
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	submission.previousReady = true
	if _, err := service.DeployApp(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &deployerv1.DeployAppRequest{RequestId: strings.Repeat("b", 64), ReportWithdrawal: true, DeployerYaml: strings.Replace(first.DeployerYaml, ":1.0.0", ":2.0.0", 1)}
	submission.drained = false
	if _, err := service.DeployApp(ctx, second); err == nil {
		t.Fatal("cleanup completed before old release drained")
	}
	lookup := &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: second.RequestId}
	metadata, err := service.GetDeployRequest(ctx, lookup)
	if err != nil || metadata.State != "applied" {
		t.Fatalf("cleanup changed committed outcome: %+v %v", metadata, err)
	}
	if len(runtime.retired) != 1 || runtime.retired[0] != first.RequestId {
		t.Fatalf("retired wrong release: %v", runtime.retired)
	}
	prepares := runtime.prepareCalls
	submission.drained = true
	metadata, err = service.AdvanceDeployRequest(ctx, lookup)
	if err != nil || metadata.State != "applied" || runtime.prepareCalls != prepares {
		t.Fatalf("cleanup retry reactivated candidate: %+v %v", metadata, err)
	}
	for _, id := range runtime.retired {
		if id != first.RequestId {
			t.Fatal("cleanup retired selected candidate")
		}
	}
	before := len(runtime.retired)
	if _, err = service.GetDeployRequest(ctx, lookup); err != nil || len(runtime.retired) != before {
		t.Fatal("read-only lookup mutated workloads")
	}
	if _, err = service.AdvanceDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: first.RequestId}); err != nil || len(runtime.retired) != before {
		t.Fatal("stale advance changed newer release")
	}
}

func TestCandidateCapacityPreservesPendingRecoveryPredecessor(t *testing.T) {
	service, _, submission, first := submissionFixture(t)
	runtime := &capacityRuntime{submissionRuntime: submission, legacyGone: true}
	service.runtime = runtime
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	submission.previousReady = true
	if _, err := service.DeployApp(ctx, first); err != nil {
		t.Fatal(err)
	}
	submission.previousReady = false
	second := &deployerv1.DeployAppRequest{RequestId: strings.Repeat("b", 64), ReportWithdrawal: true, DeployerYaml: strings.Replace(first.DeployerYaml, ":1.0.0", ":2.0.0", 1)}
	if _, err := service.DeployApp(ctx, second); err != nil {
		t.Fatal(err)
	}
	before := runtime.legacyCalls
	if _, err := service.AdvanceDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: first.RequestId}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.retired) != 0 || runtime.legacyCalls != before {
		t.Fatal("cleanup changed workloads during pending recovery window")
	}
}

func TestAppliedCandidateWaitsForLegacyDrainAndCurrentHealth(t *testing.T) {
	service, _, submission, req := submissionFixture(t)
	runtime := &capacityRuntime{submissionRuntime: submission}
	service.runtime = runtime
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	submission.previousReady = true
	if _, err := service.DeployApp(ctx, req); err == nil {
		t.Fatal("legacy workload drain was ignored")
	}
	lookup := &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: req.RequestId}
	submission.previousReady = false
	before := runtime.legacyCalls
	if _, err := service.AdvanceDeployRequest(ctx, lookup); err == nil || runtime.legacyCalls != before {
		t.Fatal("unhealthy selected release allowed further retirement")
	}
	submission.previousReady = true
	runtime.legacyGone = true
	if _, err := service.AdvanceDeployRequest(ctx, lookup); err != nil {
		t.Fatal(err)
	}
}
