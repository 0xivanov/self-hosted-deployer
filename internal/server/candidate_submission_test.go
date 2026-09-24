package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type submissionRuntime struct {
	*requestRuntime
	hasService       bool
	dependencies     int
	failDependencies bool
	initialRequestID string
	resetCalls       int
	resetLost        bool
	resetStale       bool
}

func (r *submissionRuntime) PrepareCandidateDependencies(_ context.Context, _ appconfig.Config, _ map[string]string, _, _ string, _ *registryauth.Credential) (string, error) {
	r.dependencies++
	if r.failDependencies {
		return "", ingress.ErrCandidateNotReady
	}
	return "", nil
}
func (r *submissionRuntime) CaptureActivationGate(ctx context.Context, app string) (ingress.ActivationGate, ingress.ActivationTarget, error) {
	if !r.hasService {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, app)
	}
	return r.recoveryRuntime.CaptureActivationGate(ctx, app)
}
func (r *submissionRuntime) CreateInactiveCandidateService(_ context.Context, cfg appconfig.Config, id string) error {
	selector, _ := ingress.CandidateSelector(cfg.Name, id)
	selector["deployer.io/candidate-generation"] = "inactive-" + selector["deployer.io/candidate-generation"]
	r.gate = ingress.ActivationGate{App: cfg.Name, Namespace: "apps", UID: "service-1", ResourceVersion: "1"}
	r.target = ingress.ActivationTarget{Selector: selector, Ports: []corev1.ServicePort{{Name: "http", Port: int32(cfg.Service.Port), TargetPort: intstr.FromInt32(int32(cfg.Service.Port)), Protocol: corev1.ProtocolTCP}}}
	r.initialRequestID = id
	r.hasService = true
	return nil
}
func (r *submissionRuntime) InitialCandidateRequest(context.Context, ingress.ActivationGate) (string, error) {
	return r.initialRequestID, nil
}
func (r *submissionRuntime) ResetInactiveCandidateService(_ context.Context, gate ingress.ActivationGate, cfg appconfig.Config, oldID, newID string) (ingress.ActivationGate, ingress.ActivationTarget, error) {
	if r.resetStale || gate != r.gate || oldID != r.initialRequestID {
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, ingress.ErrActivationSuperseded
	}
	r.resetCalls++
	r.initialRequestID = newID
	selector, _ := ingress.CandidateSelector(cfg.Name, newID)
	selector["deployer.io/candidate-generation"] = "inactive-" + selector["deployer.io/candidate-generation"]
	r.target = ingress.ActivationTarget{Selector: selector, Ports: []corev1.ServicePort{{Name: "http", Port: int32(cfg.Service.Port), TargetPort: intstr.FromInt32(int32(cfg.Service.Port)), Protocol: corev1.ProtocolTCP}}}
	r.gate.ResourceVersion = "reset-" + newID[:4]
	r.gate.OperationID = ingress.CandidateBootstrapOperationID(cfg.Name, newID)
	if r.resetLost {
		r.resetLost = false
		return ingress.ActivationGate{}, ingress.ActivationTarget{}, errors.New("lost reset response")
	}
	return r.gate, r.target, nil
}
func (r *submissionRuntime) ActivatePreparedCandidate(ctx context.Context, gate ingress.ActivationGate, _ appconfig.Config, _, id, _ string) (ingress.ActivationGate, error) {
	if !r.previousReady {
		return ingress.ActivationGate{}, ingress.ErrCandidateNotReady
	}
	selector, _ := ingress.CandidateSelector(gate.App, id)
	target := r.target
	target.Selector = selector
	return r.ReplaceActivationTarget(ctx, gate, target)
}
func submissionFixture(t *testing.T) (AppService, *db.Db, *submissionRuntime, *deployerv1.DeployAppRequest) {
	t.Helper()
	database := openTestDB(t)
	runtime := &submissionRuntime{requestRuntime: &requestRuntime{completionRuntime: &completionRuntime{retirementRuntime: &retirementRuntime{recoveryRuntime: &recoveryRuntime{}, drained: true}}}}
	service := NewAppService(AppServiceConfig{EnableCandidateOperations: true, DeploymentRequests: db.NewDeploymentRequestRepository(database), CandidateBindings: db.NewCandidateBindingRepository(database), CandidateCheckpoints: db.NewRuntimeCheckpointRepository(database), CandidateFinalizer: db.NewCandidateFinalizationRepository(database), Apps: db.NewAppRepository(database), Deployments: db.NewDeploymentRepository(database), Routes: db.NewRouteRepository(database), Runtime: runtime})
	return service, database, runtime, &deployerv1.DeployAppRequest{DeployerYaml: candidateHostingYAML, RequestId: strings.Repeat("a", 64), ReportWithdrawal: true}
}
func TestCandidateSubmissionAdvancesSameBoundDeployment(t *testing.T) {
	service, database, runtime, req := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	pending, err := service.DeployApp(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Deployment.Status != "pending" {
		t.Fatalf("premature success: %+v", pending)
	}
	if _, err = db.NewAppRepository(database).FindActiveByName(ctx, "hosted-api"); err == nil {
		t.Fatal("unready initial app became active")
	}
	lookup := &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: req.RequestId}
	if result, err := service.GetDeployRequest(ctx, lookup); err != nil || result.State != "pending" {
		t.Fatalf("lookup: %+v %v", result, err)
	}
	runtime.previousReady = true
	result, err := service.AdvanceDeployRequest(ctx, lookup)
	if err != nil || result.State != "applied" || result.Result.Deployment.Id != pending.Deployment.Id {
		t.Fatalf("advance: %+v %v", result, err)
	}
	if _, err = db.NewAppRepository(database).FindActiveByName(ctx, "hosted-api"); err != nil {
		t.Fatal(err)
	}
	before := runtime.prepareCalls
	replay, err := service.DeployApp(ctx, req)
	if err != nil || replay.Deployment.Id != pending.Deployment.Id || runtime.prepareCalls != before {
		t.Fatalf("submission replay: %+v %v", replay, err)
	}
	changed := &deployerv1.DeployAppRequest{RequestId: req.RequestId, ReportWithdrawal: req.ReportWithdrawal, DeployerYaml: strings.Replace(req.DeployerYaml, ":1.0.0", ":2.0.0", 1)}
	if _, err = service.DeployApp(ctx, changed); err == nil {
		t.Fatal("request ID reused with new configuration")
	}
}
func TestCandidateSubmissionPreparationFailureCanResumeByRequestID(t *testing.T) {
	service, _, runtime, req := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	runtime.failDependencies = true
	if _, err := service.DeployApp(ctx, req); err == nil {
		t.Fatal("expected preparation failure")
	}
	runtime.failDependencies = false
	runtime.previousReady = true
	result, err := service.AdvanceDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: req.RequestId})
	if err != nil || result.State != "applied" || runtime.dependencies != 2 {
		t.Fatalf("resume: %+v %v", result, err)
	}
}
func TestCandidateSubmissionInitialRecoveryKeepsAppHidden(t *testing.T) {
	service, database, _, req := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	if _, err := service.DeployApp(ctx, req); err != nil {
		t.Fatal(err)
	}
	result, err := service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: req.RequestId})
	if err != nil || result.State != "withdrawn" || !result.Result.WithdrawalConfirmed {
		t.Fatalf("recovery: %+v %v", result, err)
	}
	if _, err = db.NewAppRepository(database).FindActiveByName(ctx, "hosted-api"); err == nil {
		t.Fatal("withdrawn initial app became active")
	}
}

func TestCandidateSubmissionRetriesWithdrawnInitialCandidate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lostReset  bool
		staleGate  bool
		retireGone bool
		wantRetry  bool
	}{
		{name: "rebinds same app", wantRetry: true},
		{name: "replays lost reset", lostReset: true, wantRetry: true},
		{name: "requires retired proof", retireGone: true},
		{name: "rejects stale reset gate", staleGate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, database, runtime, firstRequest := submissionFixture(t)
			ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
			first, err := service.DeployApp(ctx, firstRequest)
			if err != nil || first.Deployment == nil {
				t.Fatalf("initial submission: %+v %v", first, err)
			}
			withdrawn, err := service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: firstRequest.RequestId})
			if err != nil || withdrawn.State != "withdrawn" {
				t.Fatalf("initial recovery: %+v %v", withdrawn, err)
			}
			runtime.resetLost = tc.lostReset
			runtime.resetStale = tc.staleGate
			runtime.drained = !tc.retireGone
			runtime.previousReady = true
			secondRequest := &deployerv1.DeployAppRequest{
				DeployerYaml:     strings.Replace(firstRequest.DeployerYaml, "port: 8080", "port: 9090", 1),
				RequestId:        strings.Repeat("b", 64),
				ReportWithdrawal: true,
			}
			second, err := service.DeployApp(ctx, secondRequest)
			if tc.lostReset {
				if err == nil || runtime.resetCalls != 1 {
					t.Fatalf("lost reset unexpectedly completed or was not recorded: %+v %v resets=%d", second, err, runtime.resetCalls)
				}
				second, err = service.DeployApp(ctx, secondRequest)
			}
			if tc.wantRetry {
				if err != nil || second.Deployment == nil || second.Deployment.AppId != first.Deployment.AppId || second.Deployment.Id == first.Deployment.Id {
					t.Fatalf("retry did not reuse app safely: %+v %v", second, err)
				}
				metadata, metadataErr := service.GetDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: secondRequest.RequestId})
				if metadataErr != nil || metadata.State != "applied" {
					t.Fatalf("retry did not reach applied state: %+v %v", metadata, metadataErr)
				}
				active, appErr := db.NewAppRepository(database).FindActiveByName(ctx, "hosted-api")
				activeConfig, configErr := appconfig.FromJSON(active.DesiredStateJSON)
				if appErr != nil || configErr != nil || activeConfig.Service.Port != 9090 {
					t.Fatalf("retry did not persist new requested port: %+v %v", active, appErr)
				}
				if tc.lostReset && runtime.resetCalls != 1 {
					t.Fatalf("lost reset was replayed: %d", runtime.resetCalls)
				}
			} else {
				if err == nil || runtime.resetCalls != 0 {
					t.Fatalf("unsafe retry was accepted: response=%+v err=%v resets=%d", second, err, runtime.resetCalls)
				}
			}
			if _, err = db.NewDeploymentRequestRepository(database).Find(ctx, "hosted-api", secondRequest.RequestId); err != nil {
				t.Fatalf("retry request journal: %v", err)
			}
		})
	}
}

func TestCandidateRecoveryAfterPriorWithdrawalBeforeCheckpoint(t *testing.T) {
	service, _, runtime, firstRequest := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	if _, err := service.DeployApp(ctx, firstRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: firstRequest.RequestId}); err != nil {
		t.Fatal(err)
	}
	secondRequest := &deployerv1.DeployAppRequest{DeployerYaml: strings.Replace(firstRequest.DeployerYaml, "port: 8080", "port: 9090", 1), RequestId: strings.Repeat("c", 64), ReportWithdrawal: true}
	runtime.failDependencies = true
	if _, err := service.DeployApp(ctx, secondRequest); err == nil {
		t.Fatal("expected second request to remain unprepared")
	}
	runtime.failDependencies = false
	result, err := service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: secondRequest.RequestId})
	if err != nil || result.State != "withdrawn" || runtime.resetCalls != 1 {
		t.Fatalf("before-checkpoint recovery did not safely rebind: %+v %v resets=%d", result, err, runtime.resetCalls)
	}
}

func TestCandidateUpdatePreservesPreviousAppUntilActivationOrRecovery(t *testing.T) {
	service, database, runtime, req := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	runtime.previousReady = true
	first, err := service.DeployApp(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	runtime.previousReady = false
	second := &deployerv1.DeployAppRequest{DeployerYaml: strings.Replace(req.DeployerYaml, ":1.0.0", ":2.0.0", 1), RequestId: strings.Repeat("b", 64), ReportWithdrawal: true}
	pending, err := service.DeployApp(ctx, second)
	if err != nil || pending.Deployment.Status != "pending" {
		t.Fatalf("update: %+v %v", pending, err)
	}
	before, err := db.NewAppRepository(database).FindActiveByName(ctx, "hosted-api")
	if err != nil || before.DesiredStateJSON != first.App.DesiredState {
		t.Fatal("pending update replaced predecessor")
	}
	runtime.previousReady = true
	result, err := service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: second.RequestId})
	if err != nil || result.State != "withdrawn" || result.Result.App.Id != first.App.Id {
		t.Fatalf("update recovery: %+v %v", result, err)
	}
	after, err := db.NewAppRepository(database).FindActiveByName(ctx, "hosted-api")
	if err != nil || after.DesiredStateJSON != before.DesiredStateJSON {
		t.Fatal("recovery changed predecessor")
	}
}

func (r *submissionRuntime) PreflightHosting(context.Context, appconfig.Config) error { return nil }

func TestCandidateSubmissionRejectsMissingEnvironmentBeforeAcceptance(t *testing.T) {
	service, database, runtime, req := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	req.DeployerYaml += "\nenvironmentRevision: " + strings.Repeat("c", 64) + "\n"
	if _, err := service.DeployApp(ctx, req); err == nil {
		t.Fatal("missing environment accepted")
	}
	if _, err := db.NewDeploymentRequestRepository(database).Find(ctx, "hosted-api", req.RequestId); err != db.ErrNotFound {
		t.Fatalf("rejected request was journaled: %v", err)
	}
	if runtime.dependencies != 0 {
		t.Fatal("rejected request mutated dependencies")
	}
}

func (r *submissionRuntime) EnsureCandidateRecoveryNamespace(context.Context) error { return nil }

func TestCandidateRecoveryBeforeCheckpointDoesNotPrepareWorkload(t *testing.T) {
	service, database, runtime, req := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	runtime.failDependencies = true
	if _, err := service.DeployApp(ctx, req); err == nil {
		t.Fatal("expected dependency preparation failure")
	}
	if _, err := db.NewRuntimeCheckpointRepository(database).FindRuntimeCheckpoint(ctx, "hosted-api", req.RequestId); err != db.ErrNotFound {
		t.Fatalf("unexpected checkpoint: %v", err)
	}
	result, err := service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: req.RequestId})
	if err != nil || result.State != "withdrawn" || !result.Result.WithdrawalConfirmed {
		t.Fatalf("unprepared recovery: %+v %v", result, err)
	}
	if runtime.dependencies != 1 || runtime.prepareCalls != 0 || runtime.retireCalls != 1 {
		t.Fatal("recovery prepared a workload or skipped retirement")
	}
	if _, err = db.NewAppRepository(database).FindActiveByName(ctx, "hosted-api"); err == nil {
		t.Fatal("unprepared recovery activated initial app")
	}
	before := runtime.retireCalls
	if _, err = service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: "hosted-api", RequestId: req.RequestId}); err != nil || runtime.retireCalls != before {
		t.Fatal("terminal recovery repeated runtime work")
	}
}

func TestUnpreparedRecoveryRejectsUnrelatedInitialService(t *testing.T) {
	service, database, runtime, req := submissionFixture(t)
	ctx := WithCaller(context.Background(), Caller{Kind: CallerAdmin})
	runtime.failDependencies = true
	if _, err := service.DeployApp(ctx, req); err == nil {
		t.Fatal("expected dependency failure")
	}
	cfg, err := appconfig.Parse([]byte(req.DeployerYaml))
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.CreateInactiveCandidateService(ctx, cfg, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RecoverDeployRequest(ctx, &deployerv1.GetDeployRequestRequest{AppName: cfg.Name, RequestId: req.RequestId}); err == nil {
		t.Fatal("unrelated initial service adopted")
	}
	if runtime.fenceCalls != 0 || runtime.retireCalls != 0 {
		t.Fatal("unrelated target mutated")
	}
	if _, err = db.NewRuntimeCheckpointRepository(database).FindRuntimeCheckpoint(ctx, cfg.Name, req.RequestId); err != db.ErrNotFound {
		t.Fatal("unrelated target recorded as predecessor")
	}
}
