package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
)

// RuntimeCheckpointRepository is the durable boundary for candidate
// activation. Callers must hold the existing per-app mutation lock.
type RuntimeCheckpointRepository interface {
	SaveActivationIntent(context.Context, string, string, string) error
	RecordActivationGate(context.Context, string, string, string, string, string) error
	FindRuntimeCheckpoint(context.Context, string, string) (domain.RuntimeCheckpoint, error)
}

// CandidateOperationRuntime exposes only the isolated candidate lifecycle.
// Legacy app reconciliation is intentionally not part of this interface.
type CandidateOperationRuntime interface {
	CaptureActivationGate(context.Context, string) (ingress.ActivationGate, ingress.ActivationTarget, error)
	FenceActivationGate(context.Context, ingress.ActivationGate, string) (ingress.ActivationGate, error)
	PrepareCandidateDeployment(context.Context, appconfig.Config, string, string, string) error
	ActivatePreparedCandidate(context.Context, ingress.ActivationGate, appconfig.Config, string, string, string) (ingress.ActivationGate, error)
}

var _ CandidateOperationRuntime = (*ingress.Controller)(nil)

type candidateOperationIntent struct {
	AppName           string                   `json:"app_name"`
	RequestID         string                   `json:"request_id"`
	RequestedState    string                   `json:"requested_state"`
	SecretRevision    string                   `json:"secret_revision"`
	RegistrySecret    string                   `json:"registry_secret"`
	InitialGate       ingress.ActivationGate   `json:"initial_gate"`
	PredecessorTarget ingress.ActivationTarget `json:"predecessor_target"`
}

// AdvanceCandidateOperation advances one pending request through the durable
// candidate stages. A prepared checkpoint from an earlier call is ambiguous;
// a fenced checkpoint resumes only with its exact saved gate and never gets a
// fresh fence token.
func AdvanceCandidateOperation(ctx context.Context, repo RuntimeCheckpointRepository, requests DeploymentRequestRepository, runtime CandidateOperationRuntime, appName, requestID string, cfg appconfig.Config, secretRevision, registrySecretName string) (ingress.ActivationGate, error) {
	if repo == nil || requests == nil || runtime == nil {
		return ingress.ActivationGate{}, errors.New("candidate operation runtime is unavailable")
	}
	if appName == "" || cfg.Name != appName || !registryauth.ValidRevision(requestID) {
		return ingress.ActivationGate{}, errors.New("candidate operation identity is invalid")
	}
	if err := cfg.Validate(); err != nil {
		return ingress.ActivationGate{}, fmt.Errorf("candidate configuration is invalid: %w", err)
	}
	if cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode {
		return ingress.ActivationGate{}, errors.New("candidate requires hosted stateless configuration")
	}
	if err := ingress.ValidateCandidateReferences(cfg, secretRevision, registrySecretName); err != nil {
		return ingress.ActivationGate{}, err
	}
	requestedState, err := cfg.JSON()
	if err != nil {
		return ingress.ActivationGate{}, errors.New("candidate configuration cannot be canonicalized")
	}
	request, err := requests.Find(ctx, appName, requestID)
	if err != nil {
		return ingress.ActivationGate{}, err
	}
	if request.AppName != appName || request.RequestID != requestID || request.State != "pending" || request.RequestedState != requestedState {
		return ingress.ActivationGate{}, errors.New("candidate operation request identity is not pending")
	}
	checkpoint, findErr := repo.FindRuntimeCheckpoint(ctx, appName, requestID)
	if errors.Is(findErr, db.ErrNotFound) {
		return startCandidateOperation(ctx, repo, runtime, appName, requestID, requestedState, cfg, secretRevision, registrySecretName)
	}
	if findErr != nil {
		return ingress.ActivationGate{}, errors.New("candidate activation checkpoint is unavailable")
	}
	if checkpoint.AppName != appName || checkpoint.RequestID != requestID {
		return ingress.ActivationGate{}, errors.New("candidate activation checkpoint identity is invalid")
	}
	intent, err := decodeCandidateIntent(checkpoint.IntentJSON)
	if err != nil || !intentMatches(intent, appName, requestID, requestedState, secretRevision, registrySecretName) || !validInitialCapture(intent.InitialGate, intent.PredecessorTarget, appName) {
		return ingress.ActivationGate{}, errors.New("candidate activation intent is ambiguous")
	}
	switch checkpoint.Stage {
	case "activated":
		gate, err := decodeGate(checkpoint.GateJSON)
		if err != nil || !validSavedGate(gate, intent.InitialGate, appName, requestID, true) {
			return ingress.ActivationGate{}, errors.New("activated candidate gate is invalid")
		}
		return gate, nil
	case "prepared":
		return ingress.ActivationGate{}, errors.New("candidate activation checkpoint requires manual recovery")
	case "fenced":
		gate, err := decodeGate(checkpoint.GateJSON)
		if err != nil || !validSavedGate(gate, intent.InitialGate, appName, requestID, true) {
			return ingress.ActivationGate{}, errors.New("fenced candidate gate is invalid")
		}
		if err := runtime.PrepareCandidateDeployment(ctx, cfg, secretRevision, requestID, registrySecretName); err != nil {
			return ingress.ActivationGate{}, err
		}
		activated, err := runtime.ActivatePreparedCandidate(ctx, gate, cfg, secretRevision, requestID, registrySecretName)
		if err != nil {
			return ingress.ActivationGate{}, err
		}
		if !validSavedGate(activated, intent.InitialGate, appName, requestID, true) {
			return ingress.ActivationGate{}, errors.New("activated candidate gate is invalid")
		}
		activatedJSON, err := canonicalCandidateJSON(activated)
		if err != nil {
			return ingress.ActivationGate{}, errors.New("activated gate cannot be recorded")
		}
		if err := recordCandidateGate(repo, ctx, appName, requestID, "fenced", "activated", activatedJSON); err != nil {
			return ingress.ActivationGate{}, errors.New("activated gate cannot be recorded")
		}
		return activated, nil
	default:
		return ingress.ActivationGate{}, errors.New("candidate activation checkpoint stage is ambiguous")
	}
}

func startCandidateOperation(ctx context.Context, repo RuntimeCheckpointRepository, runtime CandidateOperationRuntime, appName, requestID, requestedState string, cfg appconfig.Config, secretRevision, registrySecretName string) (ingress.ActivationGate, error) {
	gate, predecessor, err := runtime.CaptureActivationGate(ctx, appName)
	if err != nil {
		return ingress.ActivationGate{}, err
	}
	if !validInitialCapture(gate, predecessor, appName) {
		return ingress.ActivationGate{}, errors.New("captured activation gate is invalid")
	}
	intent := candidateOperationIntent{AppName: appName, RequestID: requestID, RequestedState: requestedState, SecretRevision: secretRevision, RegistrySecret: registrySecretName, InitialGate: gate, PredecessorTarget: predecessor}
	intentJSON, err := canonicalCandidateJSON(intent)
	if err != nil {
		return ingress.ActivationGate{}, errors.New("candidate activation intent cannot be recorded")
	}
	if err := repo.SaveActivationIntent(ctx, appName, requestID, intentJSON); err != nil {
		return ingress.ActivationGate{}, errors.New("candidate activation intent cannot be recorded")
	}
	fenced, err := runtime.FenceActivationGate(ctx, gate, requestID)
	if err != nil {
		return ingress.ActivationGate{}, err
	}
	if !validSavedGate(fenced, gate, appName, requestID, true) {
		return ingress.ActivationGate{}, errors.New("fenced activation gate is invalid")
	}
	fencedJSON, err := canonicalCandidateJSON(fenced)
	if err != nil {
		return ingress.ActivationGate{}, errors.New("fenced activation gate cannot be recorded")
	}
	if err := recordCandidateGate(repo, ctx, appName, requestID, "prepared", "fenced", fencedJSON); err != nil {
		return ingress.ActivationGate{}, errors.New("fenced activation gate cannot be recorded")
	}
	if err := runtime.PrepareCandidateDeployment(ctx, cfg, secretRevision, requestID, registrySecretName); err != nil {
		return ingress.ActivationGate{}, err
	}
	activated, err := runtime.ActivatePreparedCandidate(ctx, fenced, cfg, secretRevision, requestID, registrySecretName)
	if err != nil {
		return ingress.ActivationGate{}, err
	}
	if !validSavedGate(activated, gate, appName, requestID, true) {
		return ingress.ActivationGate{}, errors.New("activated candidate gate is invalid")
	}
	activatedJSON, err := canonicalCandidateJSON(activated)
	if err != nil {
		return ingress.ActivationGate{}, errors.New("activated gate cannot be recorded")
	}
	if err := recordCandidateGate(repo, ctx, appName, requestID, "fenced", "activated", activatedJSON); err != nil {
		return ingress.ActivationGate{}, errors.New("activated gate cannot be recorded")
	}
	return activated, nil
}

func recordCandidateGate(repo RuntimeCheckpointRepository, ctx context.Context, appName, requestID, expected, next, gateJSON string) error {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return repo.RecordActivationGate(recordCtx, appName, requestID, expected, next, gateJSON)
}

func canonicalCandidateJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeCandidateIntent(value string) (candidateOperationIntent, error) {
	var intent candidateOperationIntent
	decoder := json.NewDecoder(bytes.NewReader([]byte(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return candidateOperationIntent{}, errors.New("invalid candidate intent")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return candidateOperationIntent{}, errors.New("invalid candidate intent")
	}
	canonical, err := canonicalCandidateJSON(intent)
	if err != nil || canonical != value || intent.AppName == "" || intent.RequestID == "" {
		return candidateOperationIntent{}, errors.New("noncanonical candidate intent")
	}
	return intent, nil
}

func decodeGate(value string) (ingress.ActivationGate, error) {
	var gate ingress.ActivationGate
	decoder := json.NewDecoder(bytes.NewReader([]byte(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&gate); err != nil {
		return ingress.ActivationGate{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ingress.ActivationGate{}, errors.New("invalid activation gate")
	}
	return gate, nil
}

func validInitialCapture(gate ingress.ActivationGate, target ingress.ActivationTarget, appName string) bool {
	return gate.App == appName && gate.Namespace != "" && gate.UID != "" && gate.ResourceVersion != "" && len(target.Selector) > 0 && len(target.Ports) > 0
}

func validSavedGate(gate, initial ingress.ActivationGate, appName, requestID string, requireOperation bool) bool {
	if gate.App != appName || gate.Namespace == "" || gate.UID == "" || gate.ResourceVersion == "" || gate.UID != initial.UID || gate.Namespace != initial.Namespace {
		return false
	}
	if requireOperation && gate.OperationID != requestID {
		return false
	}
	return true
}

func intentMatches(intent candidateOperationIntent, appName, requestID, requestedState, secretRevision, registrySecretName string) bool {
	return intent.AppName == appName && intent.RequestID == requestID && intent.RequestedState == requestedState && intent.SecretRevision == secretRevision && intent.RegistrySecret == registrySecretName
}
