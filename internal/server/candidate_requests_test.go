package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type requestBinding struct {
	value domain.CandidateBinding
	err   error
}

func (b *requestBinding) Find(context.Context, string, string) (domain.CandidateBinding, error) {
	return b.value, b.err
}

type requestRuntime struct {
	*completionRuntime
	prepareCalls int
}

func (r *requestRuntime) Reconcile(context.Context, appconfig.Config, map[string]string, string) error {
	return errors.New("legacy reconcile must not run")
}
func (r *requestRuntime) Delete(context.Context, string) error {
	return errors.New("legacy delete must not run")
}
func (r *requestRuntime) Status(context.Context, string) (string, error) { return "", nil }
func (r *requestRuntime) PrepareCandidateDeployment(context.Context, appconfig.Config, string, string, string) error {
	r.prepareCalls++
	return nil
}
func (r *requestRuntime) ActivatePreparedCandidate(_ context.Context, gate ingress.ActivationGate, _ appconfig.Config, _, _, _ string) (ingress.ActivationGate, error) {
	if !r.previousReady {
		return ingress.ActivationGate{}, ingress.ErrCandidateNotReady
	}
	return gate, nil
}

type requestFinalizer struct {
	repo  *candidateOperationRepo
	calls int
}

func (f *requestFinalizer) Finalize(_ context.Context, _, _, outcome, _ string, _ bool, _ time.Time) (string, error) {
	f.calls++
	f.repo.request.State = outcome
	f.repo.request.ResponseJSON = `{"app":{"id":"app"},"deployment":{"id":"deployment"}}`
	return f.repo.request.ResponseJSON, nil
}
func requestHandlerFixture(t *testing.T) (AppService, *candidateOperationRepo, *requestRuntime, *requestFinalizer, *requestBinding, *deployerv1.GetDeployRequestRequest) {
	t.Helper()
	repo, runtime, _ := completionFixture(t)
	wrapped := &requestRuntime{completionRuntime: runtime}
	finalizer := &requestFinalizer{repo: repo}
	binding := &requestBinding{value: domain.CandidateBinding{AppName: "site", RequestID: repo.request.RequestID, AppID: "app", DeploymentID: "deployment"}}
	service := NewAppService(AppServiceConfig{EnableCandidateOperations: true, DeploymentRequests: repo, CandidateBindings: binding, CandidateCheckpoints: repo, CandidateFinalizer: finalizer, Runtime: wrapped})
	return service, repo, wrapped, finalizer, binding, &deployerv1.GetDeployRequestRequest{AppName: "site", RequestId: repo.request.RequestID}
}
func TestCandidateRequestActionsRequireAdminAndEnablement(t *testing.T) {
	for _, action := range []string{"advance", "recover"} {
		t.Run(action, func(t *testing.T) {
			service, _, runtime, finalizer, _, req := requestHandlerFixture(t)
			call := service.AdvanceDeployRequest
			if action == "recover" {
				call = service.RecoverDeployRequest
			}
			if _, err := call(context.Background(), req); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("anonymous call: %v", err)
			}
			if _, err := call(WithCaller(context.Background(), Caller{Kind: CallerAgent}), req); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("agent call: %v", err)
			}
			service.enableCandidateOperations = false
			call = service.AdvanceDeployRequest
			if action == "recover" {
				call = service.RecoverDeployRequest
			}
			if _, err := call(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), req); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("disabled call: %v", err)
			}
			if runtime.routeCalls != 0 || runtime.fenceCalls != 0 || finalizer.calls != 0 {
				t.Fatal("denied call mutated runtime")
			}
		})
	}
}
func TestCandidateAdvanceRejectsUnboundAndAmbiguousRequests(t *testing.T) {
	for _, fault := range []string{"unbound", "prepared", "recovering"} {
		t.Run(fault, func(t *testing.T) {
			service, repo, runtime, finalizer, binding, req := requestHandlerFixture(t)
			if fault == "unbound" {
				binding.err = db.ErrNotFound
			} else {
				repo.checkpoint.Stage = fault
			}
			_, err := service.AdvanceDeployRequest(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), req)
			if status.Code(err) != codes.FailedPrecondition || runtime.prepareCalls != 0 || runtime.routeCalls != 0 || finalizer.calls != 0 {
				t.Fatalf("unsafe advance: %v", err)
			}
		})
	}
}
func TestCandidateRequestAdvanceAndRecoveryReturnRecordedOutcome(t *testing.T) {
	for _, action := range []string{"advance", "recover"} {
		t.Run(action, func(t *testing.T) {
			service, _, runtime, finalizer, _, req := requestHandlerFixture(t)
			ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
			call := service.AdvanceDeployRequest
			expected := "applied"
			if action == "recover" {
				call = service.RecoverDeployRequest
				expected = "withdrawn"
			}
			result, err := call(ctx, req)
			if err != nil || result.State != expected || result.Result == nil || finalizer.calls != 1 {
				t.Fatalf("outcome: %+v %v", result, err)
			}
			routes := runtime.routeCalls
			retired := runtime.retireCalls
			if _, err = call(ctx, req); err != nil || finalizer.calls != 1 || runtime.routeCalls != routes || runtime.retireCalls != retired {
				t.Fatal("terminal replay mutated runtime")
			}
		})
	}
}
func TestCandidateAdvanceWaitingAndLookupDoNotCompleteRequest(t *testing.T) {
	service, repo, runtime, finalizer, _, req := requestHandlerFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	repo.checkpoint.Stage = "fenced"
	runtime.previousReady = false
	result, err := service.AdvanceDeployRequest(ctx, req)
	if err != nil || result.State != "pending" || finalizer.calls != 0 || runtime.routeCalls != 0 {
		t.Fatalf("waiting result: %+v %v", result, err)
	}
	prepared := runtime.prepareCalls
	if _, err = service.GetDeployRequest(ctx, req); err != nil || runtime.prepareCalls != prepared || runtime.routeCalls != 0 || finalizer.calls != 0 {
		t.Fatal("lookup mutated pending request")
	}
}
