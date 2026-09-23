package server

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	corev1 "k8s.io/api/core/v1"
)

type recoveryRuntime struct {
	gate                     ingress.ActivationGate
	target                   ingress.ActivationTarget
	fenceCalls, replaceCalls int
	loseFence, loseRestore   bool
}

func (r *recoveryRuntime) CaptureActivationGate(context.Context, string) (ingress.ActivationGate, ingress.ActivationTarget, error) {
	return r.gate, r.target, nil
}
func (r *recoveryRuntime) FenceActivationGate(_ context.Context, g ingress.ActivationGate, id string) (ingress.ActivationGate, error) {
	if g != r.gate {
		return ingress.ActivationGate{}, ingress.ErrActivationSuperseded
	}
	r.fenceCalls++
	n, _ := strconv.Atoi(g.ResourceVersion)
	r.gate.ResourceVersion = strconv.Itoa(n + 1)
	r.gate.OperationID = id
	if r.loseFence {
		r.loseFence = false
		return ingress.ActivationGate{}, errors.New("lost fence response")
	}
	return r.gate, nil
}
func (r *recoveryRuntime) ReplaceActivationTarget(_ context.Context, g ingress.ActivationGate, target ingress.ActivationTarget) (ingress.ActivationGate, error) {
	if g != r.gate {
		return ingress.ActivationGate{}, ingress.ErrActivationSuperseded
	}
	r.replaceCalls++
	n, _ := strconv.Atoi(g.ResourceVersion)
	r.gate.ResourceVersion = strconv.Itoa(n + 1)
	r.target = target
	if r.loseRestore {
		r.loseRestore = false
		return ingress.ActivationGate{}, errors.New("lost restore response")
	}
	return r.gate, nil
}

func recoveryFixture(t *testing.T) (*candidateOperationRepo, *recoveryRuntime, ingress.ActivationTarget) {
	t.Helper()
	id := strings.Repeat("a", 64)
	initial := ingress.ActivationGate{App: "site", Namespace: "apps", UID: "service", ResourceVersion: "7"}
	predecessor := ingress.ActivationTarget{Selector: map[string]string{"release": "previous"}, Ports: []corev1.ServicePort{{Port: 8080}}}
	intent := candidateOperationIntent{AppName: "site", RequestID: id, RequestedState: `{"name":"site"}`, InitialGate: initial, PredecessorTarget: predecessor}
	encoded, err := canonicalCandidateJSON(intent)
	if err != nil {
		t.Fatal(err)
	}
	r := &candidateOperationRepo{request: domain.DeployRequest{AppName: "site", RequestID: id, State: "pending", RequestedState: intent.RequestedState}, hasCheckpoint: true, checkpoint: domain.RuntimeCheckpoint{AppName: "site", RequestID: id, Stage: "fenced", IntentJSON: encoded}}
	gate := initial
	gate.ResourceVersion = "8"
	gate.OperationID = id
	return r, &recoveryRuntime{gate: gate, target: ingress.ActivationTarget{Selector: map[string]string{"release": "candidate"}, Ports: []corev1.ServicePort{{Port: 8080}}}}, predecessor
}

func TestCandidateRecoveryResumesLostRepliesAndFencesLateActivation(t *testing.T) {
	for _, lost := range []string{"none", "fence", "restore"} {
		t.Run(lost, func(t *testing.T) {
			repo, runtime, previous := recoveryFixture(t)
			oldToken := runtime.gate
			candidate := runtime.target
			runtime.loseFence = lost == "fence"
			runtime.loseRestore = lost == "restore"
			ctx := context.Background()
			_, err := RestoreCandidateTraffic(ctx, repo, repo, runtime, "site", repo.request.RequestID)
			if lost != "none" {
				if err == nil || repo.checkpoint.Stage != "recovering" {
					t.Fatalf("lost reply did not remain recoverable: %v %s", err, repo.checkpoint.Stage)
				}
				// Reconstructing the coordinator requires only persisted checkpoint state.
				_, err = RestoreCandidateTraffic(ctx, repo, repo, runtime, "site", repo.request.RequestID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if repo.checkpoint.Stage != "withdrawn" || repo.request.State != "pending" || !reflect.DeepEqual(runtime.target, previous) {
				t.Fatal("routing restoration completed request prematurely or lost predecessor")
			}
			if runtime.fenceCalls != 1 || runtime.replaceCalls != 1 {
				t.Fatalf("replayed a completed runtime mutation: %d %d", runtime.fenceCalls, runtime.replaceCalls)
			}
			if _, err = runtime.ReplaceActivationTarget(ctx, oldToken, candidate); !errors.Is(err, ingress.ErrActivationSuperseded) {
				t.Fatal("late candidate activation was accepted")
			}
			if _, err = RestoreCandidateTraffic(ctx, repo, repo, runtime, "site", repo.request.RequestID); err != nil {
				t.Fatal(err)
			}
			if runtime.fenceCalls != 1 || runtime.replaceCalls != 1 {
				t.Fatal("completed recovery replay mutated runtime")
			}
		})
	}
}

func TestCandidateRecoveryNeverOverwritesNewerOperation(t *testing.T) {
	repo, runtime, _ := recoveryFixture(t)
	runtime.gate.OperationID = strings.Repeat("b", 64)
	if _, err := RestoreCandidateTraffic(context.Background(), repo, repo, runtime, "site", repo.request.RequestID); err == nil {
		t.Fatal("foreign operation overwritten")
	}
	if runtime.fenceCalls != 0 || runtime.replaceCalls != 0 {
		t.Fatal("unrelated generation mutated")
	}
}
