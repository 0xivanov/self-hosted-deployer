package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type completionRuntime struct {
	*retirementRuntime
	routeCalls  int
	routeConfig appconfig.Config
	routeErr    error
	supersede   bool
}

func (r *completionRuntime) ReconcileCandidateRoute(_ context.Context, _ ingress.ActivationGate, _ ingress.ActivationTarget, cfg appconfig.Config) error {
	r.routeCalls++
	r.routeConfig = cfg
	if r.supersede {
		r.gate.ResourceVersion = "999"
	}
	return r.routeErr
}

type completionFinalizer struct {
	calls         int
	outcome, gate string
	err           error
}

func (f *completionFinalizer) Finalize(_ context.Context, _, _, outcome, gate string, _ bool, _ time.Time) (string, error) {
	f.calls++
	f.outcome = outcome
	f.gate = gate
	return `{"app":{"id":"app"},"deployment":{"id":"deployment"}}`, f.err
}
func completionFixture(t *testing.T) (*candidateOperationRepo, *completionRuntime, *completionFinalizer) {
	t.Helper()
	repo, base, _ := recoveryFixture(t)
	cfg, err := appconfig.Parse([]byte(candidateHostingYAML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Name = "site"
	cfg.Routing.Domain = "site.example.com"
	repo.request.RequestedState, _ = cfg.JSON()
	repo.request.PreviousAppID = "app"
	previous := cfg
	previous.Image = "example/previous:v1"
	repo.request.PreviousState, _ = previous.JSON()
	intent, err := decodeCandidateIntent(repo.checkpoint.IntentJSON)
	if err != nil {
		t.Fatal(err)
	}
	intent.RequestedState = repo.request.RequestedState
	repo.checkpoint.IntentJSON, _ = canonicalCandidateJSON(intent)
	repo.checkpoint.Stage = "activated"
	repo.checkpoint.GateJSON, _ = canonicalCandidateJSON(base.gate)
	base.target.Selector, err = ingress.CandidateSelector("site", repo.request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	return repo, &completionRuntime{retirementRuntime: &retirementRuntime{recoveryRuntime: base, drained: true, previousReady: true}}, &completionFinalizer{}
}
func TestCandidateCompletionRequiresReadySelectedGeneration(t *testing.T) {
	for _, failure := range []string{"waiting", "selector", "gate", "route", "superseded", "database", "success"} {
		t.Run(failure, func(t *testing.T) {
			repo, runtime, finalizer := completionFixture(t)
			switch failure {
			case "waiting":
				runtime.previousReady = false
			case "selector":
				runtime.target.Selector = map[string]string{"release": "old"}
			case "gate":
				runtime.gate.ResourceVersion = "other"
			case "route":
				runtime.routeErr = errors.New("route unavailable")
			case "superseded":
				runtime.supersede = true
			case "database":
				finalizer.err = errors.New("save unavailable")
			}
			response, err := CompleteCandidateOperation(context.Background(), repo, repo, finalizer, runtime, "site", repo.request.RequestID, "applied", true, time.Now())
			if failure == "success" {
				if err != nil || response == nil || finalizer.calls != 1 || finalizer.outcome != "applied" || finalizer.gate != repo.checkpoint.GateJSON || runtime.routeConfig.Routing.Domain != "site.example.com" {
					t.Fatalf("completion: %+v %v", response, err)
				}
			} else {
				if response != nil {
					t.Fatal("failed operation returned success")
				}
				if failure != "waiting" && err == nil {
					t.Fatal("missing error")
				}
				if failure != "database" && finalizer.calls != 0 {
					t.Fatal("incomplete operation committed")
				}
			}
		})
	}
}
func TestCandidateWithdrawalWaitsThenRestoresRouteBeforeCommit(t *testing.T) {
	repo, runtime, finalizer := completionFixture(t)
	runtime.drained = false
	response, err := CompleteCandidateOperation(context.Background(), repo, repo, finalizer, runtime, "site", repo.request.RequestID, "withdrawn", true, time.Now())
	if err != nil || response != nil || finalizer.calls != 0 || runtime.routeCalls != 0 {
		t.Fatalf("did not wait for drain: %v", err)
	}
	runtime.drained = true
	response, err = CompleteCandidateOperation(context.Background(), repo, repo, finalizer, runtime, "site", repo.request.RequestID, "withdrawn", true, time.Now())
	if err != nil || response == nil || finalizer.calls != 1 || finalizer.outcome != "withdrawn" || runtime.routeConfig.Image != "example/previous:v1" {
		t.Fatalf("withdrawal: %+v %v", response, err)
	}
}
func TestCandidateCompletionTerminalReplayDoesNotTouchRuntime(t *testing.T) {
	repo, runtime, finalizer := completionFixture(t)
	repo.request.State = "applied"
	repo.request.ResponseJSON = `{"app":{"id":"app"},"deployment":{"id":"deployment"}}`
	response, err := CompleteCandidateOperation(context.Background(), repo, repo, finalizer, runtime, "site", repo.request.RequestID, "applied", true, time.Now())
	if err != nil || response == nil || runtime.routeCalls != 0 || runtime.healthCalls != 0 || finalizer.calls != 0 {
		t.Fatalf("terminal replay: %v", err)
	}
}

func TestInitialCandidateWithdrawalRemovesRouteOnlyAfterRetirement(t *testing.T) {
	repo, runtime, finalizer := completionFixture(t)
	repo.request.PreviousAppID = ""
	repo.request.PreviousState = ""
	intent, err := decodeCandidateIntent(repo.checkpoint.IntentJSON)
	if err != nil {
		t.Fatal(err)
	}
	selector, _ := ingress.CandidateSelector("site", repo.request.RequestID)
	selector["deployer.io/candidate-generation"] = "inactive-" + selector["deployer.io/candidate-generation"]
	intent.PredecessorTarget = ingress.ActivationTarget{Selector: selector, Ports: []corev1.ServicePort{{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP}}}
	repo.checkpoint.IntentJSON, _ = canonicalCandidateJSON(intent)
	response, err := CompleteCandidateOperation(context.Background(), repo, repo, finalizer, runtime, "site", repo.request.RequestID, "withdrawn", true, time.Now())
	if err != nil || response == nil || finalizer.calls != 1 || runtime.routeCalls != 1 || runtime.routeConfig.Routing.Domain != "" || runtime.retireCalls != 1 {
		t.Fatalf("initial withdrawal: response=%v error=%v finalizations=%d routes=%d", response, err, finalizer.calls, runtime.routeCalls)
	}
}
