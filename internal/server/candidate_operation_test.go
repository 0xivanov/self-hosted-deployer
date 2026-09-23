package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

type candidateOperationRepo struct {
	request         domain.DeployRequest
	checkpoint      domain.RuntimeCheckpoint
	hasCheckpoint   bool
	failFenceRecord bool
}

func (r *candidateOperationRepo) Find(_ context.Context, _, _ string) (domain.DeployRequest, error) {
	return r.request, nil
}
func (r *candidateOperationRepo) Begin(context.Context, domain.DeployRequest) (domain.DeployRequest, bool, error) {
	return r.request, false, nil
}
func (r *candidateOperationRepo) Complete(context.Context, string, string, string, string, time.Time) error {
	return nil
}
func (r *candidateOperationRepo) PendingByApp(context.Context, string) (domain.DeployRequest, error) {
	return r.request, nil
}
func (r *candidateOperationRepo) SaveActivationIntent(_ context.Context, app, id, intent string) error {
	if r.hasCheckpoint {
		return nil
	}
	r.hasCheckpoint = true
	r.checkpoint = domain.RuntimeCheckpoint{AppName: app, RequestID: id, Stage: "prepared", IntentJSON: intent, GateJSON: "{}"}
	return nil
}
func (r *candidateOperationRepo) RecordActivationGate(_ context.Context, _, _ string, expected, next, gate string) error {
	if r.failFenceRecord && next == "fenced" {
		return errors.New("storage unavailable")
	}
	if !r.hasCheckpoint || r.checkpoint.Stage != expected {
		return errors.New("stage conflict")
	}
	r.checkpoint.Stage, r.checkpoint.GateJSON = next, gate
	return nil
}
func (r *candidateOperationRepo) FindRuntimeCheckpoint(_ context.Context, _, _ string) (domain.RuntimeCheckpoint, error) {
	if !r.hasCheckpoint {
		return domain.RuntimeCheckpoint{}, db.ErrNotFound
	}
	return r.checkpoint, nil
}

type candidateOperationRuntime struct {
	gate                                    ingress.ActivationGate
	target                                  ingress.ActivationTarget
	failActivation                          bool
	captures, fences, prepares, activations int
}

func (r *candidateOperationRuntime) CaptureActivationGate(context.Context, string) (ingress.ActivationGate, ingress.ActivationTarget, error) {
	r.captures++
	return r.gate, r.target, nil
}
func (r *candidateOperationRuntime) FenceActivationGate(_ context.Context, gate ingress.ActivationGate, id string) (ingress.ActivationGate, error) {
	r.fences++
	gate.OperationID = id
	return gate, nil
}
func (r *candidateOperationRuntime) PrepareCandidateDeployment(context.Context, appconfig.Config, string, string, string) error {
	r.prepares++
	return nil
}
func (r *candidateOperationRuntime) ActivatePreparedCandidate(_ context.Context, gate ingress.ActivationGate, _ appconfig.Config, _, _, _ string) (ingress.ActivationGate, error) {
	r.activations++
	if r.failActivation {
		return ingress.ActivationGate{}, errors.New("candidate not ready")
	}
	return gate, nil
}

func TestAdvanceCandidateOperationPersistsStagesAndReplaysActivatedGate(t *testing.T) {
	cfg, err := appconfig.Parse([]byte(candidateHostingYAML))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	requestID := strings.Repeat("a", 64)
	requestedState, err := cfg.JSON()
	if err != nil {
		t.Fatalf("canonical config: %v", err)
	}
	repo := &candidateOperationRepo{request: domain.DeployRequest{AppName: cfg.Name, RequestID: requestID, State: "pending", RequestedState: requestedState}}
	runtime := &candidateOperationRuntime{gate: ingress.ActivationGate{App: cfg.Name, Namespace: "deployer-system", UID: types.UID("service-1"), ResourceVersion: "7"}, target: ingress.ActivationTarget{Selector: map[string]string{"deployer.io/app": cfg.Name}, Ports: []corev1.ServicePort{{Port: 8080}}}}
	gate, err := AdvanceCandidateOperation(context.Background(), repo, repo, runtime, cfg.Name, requestID, cfg, "", "")
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if repo.checkpoint.Stage != "activated" || gate.OperationID != requestID || runtime.captures != 1 || runtime.fences != 1 || runtime.prepares != 1 || runtime.activations != 1 {
		t.Fatalf("unexpected first advance state: checkpoint=%#v gate=%#v runtime=%#v", repo.checkpoint, gate, runtime)
	}
	if _, err := AdvanceCandidateOperation(context.Background(), repo, repo, runtime, cfg.Name, requestID, cfg, "", ""); err != nil {
		t.Fatalf("activated replay: %v", err)
	}
	if runtime.captures != 1 || runtime.fences != 1 || runtime.prepares != 1 || runtime.activations != 1 {
		t.Fatal("activated replay mutated runtime")
	}
}

func TestAdvanceCandidateOperationFailsClosedForPreparedCheckpoint(t *testing.T) {
	cfg, err := appconfig.Parse([]byte(candidateHostingYAML))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	requestID := strings.Repeat("b", 64)
	requestedState, _ := cfg.JSON()
	repo := &candidateOperationRepo{request: domain.DeployRequest{AppName: cfg.Name, RequestID: requestID, State: "pending", RequestedState: requestedState}, hasCheckpoint: true, checkpoint: domain.RuntimeCheckpoint{AppName: cfg.Name, RequestID: requestID, Stage: "prepared", IntentJSON: `{"app_name":"other"}`}}
	runtime := &candidateOperationRuntime{}
	if _, err := AdvanceCandidateOperation(context.Background(), repo, repo, runtime, cfg.Name, requestID, cfg, "", ""); err == nil {
		t.Fatal("prepared checkpoint unexpectedly replayed")
	}
	if runtime.captures != 0 || runtime.fences != 0 {
		t.Fatal("ambiguous checkpoint mutated runtime")
	}
}

func TestAdvanceCandidateOperationDoesNotPrepareWhenFenceReceiptCannotPersist(t *testing.T) {
	cfg, err := appconfig.Parse([]byte(candidateHostingYAML))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	requestID := strings.Repeat("c", 64)
	requestedState, _ := cfg.JSON()
	repo := &candidateOperationRepo{request: domain.DeployRequest{AppName: cfg.Name, RequestID: requestID, State: "pending", RequestedState: requestedState}, failFenceRecord: true}
	runtime := &candidateOperationRuntime{gate: ingress.ActivationGate{App: cfg.Name, Namespace: "deployer-system", UID: types.UID("service-1"), ResourceVersion: "7"}, target: ingress.ActivationTarget{Selector: map[string]string{"deployer.io/app": cfg.Name}, Ports: []corev1.ServicePort{{Port: 8080}}}}
	if _, err := AdvanceCandidateOperation(context.Background(), repo, repo, runtime, cfg.Name, requestID, cfg, "", ""); err == nil {
		t.Fatal("expected fence receipt persistence failure")
	}
	if runtime.prepares != 0 || runtime.activations != 0 {
		t.Fatal("candidate advanced without durable fence receipt")
	}
}

func TestAdvanceCandidateOperationRejectsInvalidCapturedGate(t *testing.T) {
	cfg, err := appconfig.Parse([]byte(candidateHostingYAML))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	requestID := strings.Repeat("e", 64)
	requestedState, _ := cfg.JSON()
	repo := &candidateOperationRepo{request: domain.DeployRequest{AppName: cfg.Name, RequestID: requestID, State: "pending", RequestedState: requestedState}}
	runtime := &candidateOperationRuntime{gate: ingress.ActivationGate{App: cfg.Name, Namespace: "deployer-system", ResourceVersion: "7"}, target: ingress.ActivationTarget{Selector: map[string]string{"deployer.io/app": cfg.Name}, Ports: []corev1.ServicePort{{Port: 8080}}}}
	if _, err := AdvanceCandidateOperation(context.Background(), repo, repo, runtime, cfg.Name, requestID, cfg, "", ""); err == nil {
		t.Fatal("invalid gate was accepted")
	}
	if runtime.fences != 0 || repo.hasCheckpoint {
		t.Fatal("invalid gate advanced or persisted")
	}
}

func TestAdvanceCandidateOperationResumesFencedStageWithSavedGate(t *testing.T) {
	cfg, err := appconfig.Parse([]byte(candidateHostingYAML))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	requestID := strings.Repeat("d", 64)
	requestedState, _ := cfg.JSON()
	repo := &candidateOperationRepo{request: domain.DeployRequest{AppName: cfg.Name, RequestID: requestID, State: "pending", RequestedState: requestedState}}
	runtime := &candidateOperationRuntime{gate: ingress.ActivationGate{App: cfg.Name, Namespace: "deployer-system", UID: types.UID("service-1"), ResourceVersion: "7"}, target: ingress.ActivationTarget{Selector: map[string]string{"deployer.io/app": cfg.Name}, Ports: []corev1.ServicePort{{Port: 8080}}}, failActivation: true}
	if _, err := AdvanceCandidateOperation(context.Background(), repo, repo, runtime, cfg.Name, requestID, cfg, "", ""); err == nil {
		t.Fatal("expected readiness failure")
	}
	if repo.checkpoint.Stage != "fenced" {
		t.Fatalf("expected fenced checkpoint, got %q", repo.checkpoint.Stage)
	}
	runtime.failActivation = false
	if _, err := AdvanceCandidateOperation(context.Background(), repo, repo, runtime, cfg.Name, requestID, cfg, "", ""); err != nil {
		t.Fatalf("resume fenced operation: %v", err)
	}
	if runtime.captures != 1 || runtime.fences != 1 || runtime.prepares != 2 || runtime.activations != 2 {
		t.Fatalf("resume recaptured or refenced: %#v", runtime)
	}
}

const candidateHostingYAML = `
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
