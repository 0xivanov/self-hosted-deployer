package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

type CandidateRecoveryRuntime interface {
	CaptureActivationGate(context.Context, string) (ingress.ActivationGate, ingress.ActivationTarget, error)
	FenceActivationGate(context.Context, ingress.ActivationGate, string) (ingress.ActivationGate, error)
	ReplaceActivationTarget(context.Context, ingress.ActivationGate, ingress.ActivationTarget) (ingress.ActivationGate, error)
}

var _ CandidateRecoveryRuntime = (*ingress.Controller)(nil)

type candidateRecoveryIntent struct {
	RecoveryID string `json:"recovery_id"`
}

func candidateRecoveryID(app, request string) string {
	sum := sha256.Sum256([]byte("candidate-recovery-v1\x00" + app + "\x00" + request))
	return hex.EncodeToString(sum[:])
}

// RestoreCandidateTraffic durably fences a pending operation and restores its
// saved predecessor selector and ports. Callers must hold the app mutation lock
// and exclude legacy stable-resource writers. A withdrawn checkpoint proves
// routing compensation only: retiring/draining candidate pods, predecessor health
// and application/route records remain required before completing the request.
func RestoreCandidateTraffic(ctx context.Context, repo RuntimeCheckpointRepository, requests DeploymentRequestRepository, runtime CandidateRecoveryRuntime, app, requestID string) (ingress.ActivationGate, error) {
	fail := func() (ingress.ActivationGate, error) {
		return ingress.ActivationGate{}, errors.New("candidate recovery identity is ambiguous")
	}
	if err := ctx.Err(); err != nil {
		return ingress.ActivationGate{}, err
	}
	if repo == nil || requests == nil || runtime == nil || app == "" || !registryauth.ValidRevision(requestID) {
		return fail()
	}
	request, err := requests.Find(ctx, app, requestID)
	if err != nil {
		return ingress.ActivationGate{}, err
	}
	if request.AppName != app || request.RequestID != requestID || request.State != "pending" {
		return fail()
	}
	checkpoint, err := repo.FindRuntimeCheckpoint(ctx, app, requestID)
	if err != nil {
		return ingress.ActivationGate{}, err
	}
	if checkpoint.AppName != app || checkpoint.RequestID != requestID {
		return fail()
	}
	intent, err := decodeCandidateIntent(checkpoint.IntentJSON)
	if err != nil || intent.AppName != app || intent.RequestID != requestID || intent.RequestedState != request.RequestedState || !validInitialCapture(intent.InitialGate, intent.PredecessorTarget, app) {
		return fail()
	}
	recoveryID := candidateRecoveryID(app, requestID)
	if recoveryID == requestID || recoveryID == intent.InitialGate.OperationID {
		return fail()
	}
	savedRecovery, _ := canonicalCandidateJSON(candidateRecoveryIntent{RecoveryID: recoveryID})
	switch checkpoint.Stage {
	case "prepared", "fenced", "activated":
		// The recovery identity is persisted BEFORE any Kubernetes mutation.
		if err = recordCandidateGate(repo, ctx, app, requestID, checkpoint.Stage, "recovering", savedRecovery); err != nil {
			return ingress.ActivationGate{}, err
		}
	case "recovering":
		var recovery candidateRecoveryIntent
		if json.Unmarshal([]byte(checkpoint.GateJSON), &recovery) != nil || checkpoint.GateJSON != savedRecovery || recovery.RecoveryID != recoveryID {
			return fail()
		}
	case "withdrawn":
		gate, err := decodeGate(checkpoint.GateJSON)
		if err != nil || !validSavedGate(gate, intent.InitialGate, app, recoveryID, true) {
			return fail()
		}
		return gate, nil
	default:
		return fail()
	}
	current, target, err := runtime.CaptureActivationGate(ctx, app)
	if err != nil {
		return ingress.ActivationGate{}, err
	}
	if !validSavedGate(current, intent.InitialGate, app, recoveryID, false) {
		return fail()
	}
	if current.OperationID != intent.InitialGate.OperationID && current.OperationID != requestID && current.OperationID != recoveryID {
		return fail()
	}
	// Unlike a deployment retry, recovery may observe its own durable recovery
	// identity after a lost reply. Every action under that identity restores the
	// same saved target; it can never activate the abandoned candidate.
	if current.OperationID != recoveryID {
		current, err = runtime.FenceActivationGate(ctx, current, recoveryID)
		if err != nil {
			return ingress.ActivationGate{}, err
		}
		if !validSavedGate(current, intent.InitialGate, app, recoveryID, true) {
			return fail()
		}
		// The fence preserves the selector, but always restore after acquiring it.
		target = ingress.ActivationTarget{}
	}
	if !apiequality.Semantic.DeepEqual(target, intent.PredecessorTarget) {
		current, err = runtime.ReplaceActivationTarget(ctx, current, intent.PredecessorTarget)
		if err != nil {
			return ingress.ActivationGate{}, err
		}
		if !validSavedGate(current, intent.InitialGate, app, recoveryID, true) {
			return fail()
		}
	}
	gateJSON, err := canonicalCandidateJSON(current)
	if err != nil {
		return ingress.ActivationGate{}, err
	}
	if err = recordCandidateGate(repo, ctx, app, requestID, "recovering", "withdrawn", gateJSON); err != nil {
		return ingress.ActivationGate{}, err
	}
	return current, nil
}
