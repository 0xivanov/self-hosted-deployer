package server

import (
	"context"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
)

type retirementRuntime struct {
	*recoveryRuntime
	drained, previousReady   bool
	retireCalls, healthCalls int
}

func (r *retirementRuntime) RetireCandidateDeployment(context.Context, appconfig.Config, string, string, string) error {
	r.retireCalls++
	return nil
}
func (r *retirementRuntime) CandidateRetired(context.Context, appconfig.Config, string, string, string) (bool, error) {
	return r.drained, nil
}
func (r *retirementRuntime) RestoredCandidateTargetReady(context.Context, ingress.ActivationGate, ingress.ActivationTarget, appconfig.Config) (bool, error) {
	r.healthCalls++
	return r.previousReady, nil
}

func TestRecoverCandidateWaitsForDrainAndPredecessorBeforeFinalization(t *testing.T) {
	repo, base, _ := recoveryFixture(t)
	cfg, err := appconfig.Parse([]byte(candidateHostingYAML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Name = "site"
	repo.request.RequestedState, err = cfg.JSON()
	if err != nil {
		t.Fatal(err)
	}
	intent, err := decodeCandidateIntent(repo.checkpoint.IntentJSON)
	if err != nil {
		t.Fatal(err)
	}
	intent.RequestedState = repo.request.RequestedState
	repo.checkpoint.IntentJSON, err = canonicalCandidateJSON(intent)
	if err != nil {
		t.Fatal(err)
	}
	previous := cfg
	previous.Image = "example/previous:v1"
	repo.request.PreviousAppID = "previous-app"
	repo.request.PreviousState, err = previous.JSON()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &retirementRuntime{recoveryRuntime: base}
	ctx := context.Background()
	ready, err := RecoverCandidateOperation(ctx, repo, repo, runtime, "site", repo.request.RequestID)
	if err != nil || ready || runtime.healthCalls != 0 {
		t.Fatalf("did not wait for candidate drain: %v %v", ready, err)
	}
	runtime.drained = true
	ready, err = RecoverCandidateOperation(ctx, repo, repo, runtime, "site", repo.request.RequestID)
	if err != nil || ready || runtime.healthCalls != 1 {
		t.Fatalf("did not wait for predecessor: %v %v", ready, err)
	}
	runtime.previousReady = true
	ready, err = RecoverCandidateOperation(ctx, repo, repo, runtime, "site", repo.request.RequestID)
	if err != nil || !ready {
		t.Fatalf("ready recovery rejected: %v %v", ready, err)
	}
	if repo.request.State != "pending" {
		t.Fatal("runtime helper completed request without app/route bookkeeping")
	}
	if base.fenceCalls != 1 || base.replaceCalls != 1 {
		t.Fatal("readiness retry repeated traffic recovery")
	}
}
