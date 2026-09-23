package server

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

var _ CandidateCompletionRuntime = (*ingress.Controller)(nil)

// CandidateFinalizer is the single database commit boundary. It rechecks the
// request, binding, checkpoint and predecessor in the same transaction.
type CandidateFinalizer interface {
	Finalize(context.Context, string, string, string, string, bool, time.Time) (string, error)
}

type CandidateCompletionRuntime interface {
	CandidateRetirementRuntime
	ReconcileCandidateRoute(context.Context, ingress.ActivationGate, ingress.ActivationTarget, appconfig.Config) error
}

// CompleteCandidateOperation observes the exact selected workload, reconciles
// its public route, then records the outcome atomically. Hold the app mutation
// lock throughout. Old runtime writers must be stopped before enabling this
// path. A nil response without an error means readiness/recovery is still pending.
// Applied records are not a claim that public DNS or HTTPS is already ready.
func CompleteCandidateOperation(ctx context.Context, checkpoints RuntimeCheckpointRepository, requests DeploymentRequestRepository, finalizer CandidateFinalizer, runtime CandidateCompletionRuntime, app, requestID, outcome string, tlsEnabled bool, now time.Time) (*deployerv1.DeployAppResponse, error) {
	if checkpoints == nil || requests == nil || finalizer == nil || runtime == nil || app == "" || !registryauth.ValidRevision(requestID) || (outcome != "applied" && outcome != "withdrawn") || now.IsZero() {
		return nil, errors.New("candidate completion is not configured or invalid")
	}
	request, err := requests.Find(ctx, app, requestID)
	if err != nil {
		return nil, err
	}
	if request.AppName != app || request.RequestID != requestID {
		return nil, errors.New("candidate completion request identity changed")
	}
	if request.State != "pending" {
		if request.State != outcome {
			return nil, errors.New("candidate outcome is already terminal")
		}
		return decodeCandidateReply(request.ResponseJSON)
	}
	cfg, err := appconfig.FromJSON(request.RequestedState)
	if err != nil || cfg.Validate() != nil || cfg.Name != app || cfg.Hosting == nil || cfg.State.Mode != appconfig.DefaultStateMode {
		return nil, errors.New("candidate completion configuration is invalid")
	}
	if (request.PreviousAppID == "") != (request.PreviousState == "") {
		return nil, errors.New("candidate predecessor is incomplete")
	}
	checkpoint, err := checkpoints.FindRuntimeCheckpoint(ctx, app, requestID)
	if err != nil {
		return nil, err
	}
	intent, err := decodeCandidateIntent(checkpoint.IntentJSON)
	if err != nil || !intentMatches(intent, app, requestID, request.RequestedState, cfg.EnvironmentRevision, ingress.CandidateRegistrySecretName(cfg)) {
		return nil, errors.New("candidate completion intent changed")
	}
	if outcome == "withdrawn" {
		recovered, recoveryErr := RecoverCandidateOperation(ctx, checkpoints, requests, runtime, app, requestID)
		if recoveryErr != nil || !recovered {
			return nil, recoveryErr
		}
		beforeIntent := checkpoint.IntentJSON
		checkpoint, err = checkpoints.FindRuntimeCheckpoint(ctx, app, requestID)
		if err != nil {
			return nil, err
		}
		if checkpoint.IntentJSON != beforeIntent {
			return nil, errors.New("candidate intent changed during recovery")
		}
	}
	wantedStage := "activated"
	operationID := requestID
	if outcome == "withdrawn" {
		wantedStage = "withdrawn"
		operationID = candidateRecoveryID(app, requestID)
	}
	gate, err := decodeGate(checkpoint.GateJSON)
	if err != nil || checkpoint.AppName != app || checkpoint.RequestID != requestID || checkpoint.Stage != wantedStage || !validSavedGate(gate, intent.InitialGate, app, operationID, true) {
		return nil, errors.New("candidate completion checkpoint is invalid")
	}
	current, target, err := runtime.CaptureActivationGate(ctx, app)
	if err != nil {
		return nil, err
	}
	if current != gate {
		return nil, errors.New("candidate traffic changed before completion")
	}
	routeConfig := cfg
	if outcome == "applied" {
		expectedSelector, selectorErr := ingress.CandidateSelector(app, requestID)
		if selectorErr != nil || !maps.Equal(target.Selector, expectedSelector) {
			return nil, errors.New("candidate generation is not selected")
		}
		ready, readyErr := runtime.RestoredCandidateTargetReady(ctx, gate, target, cfg)
		if readyErr != nil || !ready {
			return nil, readyErr
		}
	} else {
		if !apiequality.Semantic.DeepEqual(target, intent.PredecessorTarget) {
			return nil, errors.New("candidate predecessor target changed")
		}
		if request.PreviousAppID != "" {
			routeConfig, err = appconfig.FromJSON(request.PreviousState)
			if err != nil || routeConfig.Validate() != nil || routeConfig.Name != app {
				return nil, errors.New("candidate predecessor configuration is invalid")
			}
		} else {
			routeConfig.Routing.Domain = ""
		}
	}
	if err = runtime.ReconcileCandidateRoute(ctx, gate, target, routeConfig); err != nil {
		return nil, err
	}
	// Route writes address another Kubernetes object. Reobserve the Service so a
	// superseded operation cannot commit its receipt after that write.
	current, afterTarget, err := runtime.CaptureActivationGate(ctx, app)
	if err != nil {
		return nil, err
	}
	if current != gate || !apiequality.Semantic.DeepEqual(target, afterTarget) {
		return nil, errors.New("candidate traffic changed during route reconciliation")
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, err := finalizer.Finalize(saveCtx, app, requestID, outcome, checkpoint.GateJSON, tlsEnabled, now)
	if err != nil {
		return nil, err
	}
	return decodeCandidateReply(result)
}

func decodeCandidateReply(encoded string) (*deployerv1.DeployAppResponse, error) {
	var reply deployerv1.DeployAppResponse
	if json.Unmarshal([]byte(encoded), &reply) != nil || reply.App == nil || reply.Deployment == nil {
		return nil, errors.New("saved candidate response is invalid")
	}
	return &reply, nil
}
