package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// This opt-in check uses a fresh namespace and database; it never points the
// candidate implementation at existing app namespaces or the live database.
func TestCandidateRealClusterLifecycle(t *testing.T) {
	kubeconfig := os.Getenv("DEPLOYER_CANDIDATE_CHECK_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("explicit cluster qualification kubeconfig required")
	}
	image := os.Getenv("DEPLOYER_CANDIDATE_CHECK_IMAGE")
	if !strings.Contains(image, "@sha256:") {
		t.Fatal("a pinned ARM64 unprivileged HTTP image on port 8080 is required")
	}
	ctx, cancel := context.WithTimeout(WithCaller(context.Background(), Caller{Kind: CallerAdmin}), 8*time.Minute)
	defer cancel()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "launchstead-candidate-check-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("isolated namespace: %s", ns.Name)
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if t.Failed() {
			pods, _ := client.CoreV1().Pods(ns.Name).List(cleanup, metav1.ListOptions{})
			if pods != nil {
				for _, p := range pods.Items {
					t.Logf("pod %s on %s: phase=%s conditions=%+v", p.Name, p.Spec.NodeName, p.Status.Phase, p.Status.Conditions)
				}
			}
		}
		if err := client.CoreV1().Namespaces().Delete(cleanup, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &ns.UID}}); err != nil {
			t.Errorf("namespace cleanup: %v", err)
		}
	})
	controller, err := ingress.NewController(ingress.ControllerConfig{KubeconfigPath: kubeconfig, Namespace: ns.Name})
	if err != nil {
		t.Fatal(err)
	}
	database := openTestDB(t)
	service := NewAppService(AppServiceConfig{EnableCandidateOperations: true, DeploymentRequests: db.NewDeploymentRequestRepository(database), CandidateBindings: db.NewCandidateBindingRepository(database), CandidateCheckpoints: db.NewRuntimeCheckpointRepository(database), CandidateFinalizer: db.NewCandidateFinalizationRepository(database), Apps: db.NewAppRepository(database), Deployments: db.NewDeploymentRepository(database), Routes: db.NewRouteRepository(database), Runtime: controller})
	spec := fmt.Sprintf(`name: candidate-check
image: %s
service:
  port: 8080
  health: {path: /}
routing: {}
deploy: {replicas: 2, strategy: rolling}
placement: {arch: linux/arm64, spread: true}
state: {mode: stateless}
resilience: {mode: resilient}
hosting:
  version: v1
  maxReplicas: 2
  resources:
    requests: {cpu: 50m, memory: 64Mi, ephemeralStorage: 64Mi}
    limits: {cpu: 500m, memory: 256Mi, ephemeralStorage: 256Mi}
`, image)
	request := func(letter string, yaml string) *deployerv1.DeployAppRequest {
		return &deployerv1.DeployAppRequest{DeployerYaml: yaml, RequestId: strings.Repeat(letter, 64), ReportWithdrawal: true}
	}
	lookup := func(letter string) *deployerv1.GetDeployRequestRequest {
		return &deployerv1.GetDeployRequestRequest{AppName: "candidate-check", RequestId: strings.Repeat(letter, 64)}
	}
	wait := func(label string, check func() error) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		var last error
		for time.Now().Before(deadline) && ctx.Err() == nil {
			last = check()
			if last == nil {
				t.Log(label + " confirmed")
				return
			}
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
		}
		t.Fatalf("%s: %v", label, last)
	}
	wait("absent request withdrawal", func() error {
		m, e := service.WithdrawDeployRequest(ctx, request("a", spec))
		if e == nil && m.State != "withdrawn" {
			return fmt.Errorf("outcome %s", m.State)
		}
		return e
	})
	if _, err = service.DeployApp(ctx, request("b", spec)); err != nil {
		t.Fatal("publish initial:", err)
	}
	wait("initial publication", func() error {
		m, e := service.AdvanceDeployRequest(ctx, lookup("b"))
		if e == nil && m.State != "applied" {
			return fmt.Errorf("outcome %s", m.State)
		}
		return e
	})
	pods, err := client.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]bool{}
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			nodes[p.Spec.NodeName] = true
		}
	}
	if len(nodes) != 2 {
		t.Fatalf("expected both ARM64 workers, got %v", nodes)
	}
	bad := strings.Replace(spec, "health: {path: /}", "health: {path: /qualification-missing}", 1)
	if _, err = service.DeployApp(ctx, request("c", bad)); err != nil {
		t.Fatal("publish unhealthy candidate:", err)
	}
	wait("unhealthy update recovery", func() error {
		m, e := service.RecoverDeployRequest(ctx, lookup("c"))
		if e == nil && m.State != "withdrawn" {
			return fmt.Errorf("outcome %s", m.State)
		}
		return e
	})
	if _, err = service.AdvanceDeployRequest(ctx, lookup("c")); err == nil {
		t.Fatal("withdrawn update advanced")
	}
	if state, err := controller.Status(ctx, "candidate-check"); err != nil || state != ingress.StatusHealthy {
		t.Fatalf("predecessor health: %s %v", state, err)
	}
	wait("candidate project deletion", func() error {
		_, e := service.DeleteApp(ctx, &deployerv1.DeleteAppRequest{Name: "candidate-check"})
		return e
	})
	pods, err = client.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
	if err != nil || len(pods.Items) != 0 {
		t.Fatalf("deleted project still has Pods: %v %v", pods, err)
	}
}
