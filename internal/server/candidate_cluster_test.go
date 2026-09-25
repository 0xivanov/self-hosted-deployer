package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/ingress"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/security"
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
	tls := ingress.TLSConfig{}
	domain := os.Getenv("DEPLOYER_CANDIDATE_CHECK_DOMAIN")
	if domain != "" {
		if !strings.HasPrefix(domain, "launchstead-check-") || !strings.HasSuffix(domain, ".0xivanov.dev") {
			t.Fatal("qualification domain must be an isolated launchstead-check-*.0xivanov.dev hostname")
		}
		tls = ingress.TLSConfig{ACMEEmail: os.Getenv("DEPLOYER_CANDIDATE_CHECK_ACME_EMAIL")}
		if !tls.Enabled() {
			t.Fatal("explicit ACME email required for HTTPS check")
		}
	}
	controller, err := ingress.NewController(ingress.ControllerConfig{KubeconfigPath: kubeconfig, Namespace: ns.Name, TLS: tls})
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "candidate.db")
	database, err := db.Open(ctx, "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if database != nil {
			_ = database.Close()
		}
	})
	key := make([]byte, security.SecretKeyBytes)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher, err := security.NewSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	registryService := func() *RegistryCredentialService {
		return NewRegistryCredentialService(db.NewRegistryCredentialRepository(database), cipher)
	}
	newService := func() AppService {
		return NewAppService(AppServiceConfig{EnableCandidateOperations: true, RegistryCredentialRepository: db.NewRegistryCredentialRepository(database), RegistryCredentials: registryService(), DeploymentRequests: db.NewDeploymentRequestRepository(database), CandidateBindings: db.NewCandidateBindingRepository(database), CandidateCheckpoints: db.NewRuntimeCheckpointRepository(database), CandidateFinalizer: db.NewCandidateFinalizationRepository(database), Apps: db.NewAppRepository(database), Deployments: db.NewDeploymentRepository(database), Routes: db.NewRouteRepository(database), Runtime: controller, RouteTLSEnabled: tls.Enabled()})
	}
	service := newService()
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
	credentialRevision := ""
	if path := os.Getenv("DEPLOYER_CANDIDATE_CHECK_REGISTRY_FILE"); path != "" {
		username, password := readCandidateCheckCredential(t, path)
		credentialRevision = strings.Repeat("e", 64)
		if _, err = registryService().CreateRegistryCredential(ctx, &deployerv1.CreateRegistryCredentialRequest{AppName: "candidate-check", Revision: credentialRevision, Registry: "ghcr.io", Username: username, Password: password}); err != nil {
			t.Fatal("register isolated pull credential:", err)
		}
		spec = "imagePullCredential: " + credentialRevision + "\n" + spec
	}
	if domain != "" {
		spec = strings.Replace(spec, "routing: {}", "routing: {domain: "+domain+"}", 1)
	}
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
	checkHTTPS := func() error {
		if domain == "" {
			return nil
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil)
		if err != nil {
			return err
		}
		httpClient := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := httpClient.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
		if err != nil {
			return err
		}
		if response.StatusCode != 200 || !strings.Contains(string(body), "Welcome to nginx!") {
			return fmt.Errorf("unexpected HTTPS response: status %d", response.StatusCode)
		}
		return nil
	}
	if domain != "" {
		wait("public HTTPS", checkHTTPS)
	}
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
	lateController, err := ingress.NewController(ingress.ControllerConfig{KubeconfigPath: kubeconfig, Namespace: ns.Name, TLS: tls})
	if err != nil {
		t.Fatal(err)
	}
	lateGate, lateTarget, err := lateController.CaptureActivationGate(ctx, "candidate-check")
	if err != nil {
		t.Fatal(err)
	}
	lateTarget.Selector, err = ingress.CandidateSelector("candidate-check", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	wait("unhealthy update recovery", func() error {
		m, e := service.RecoverDeployRequest(ctx, lookup("c"))
		if e == nil && m.State != "withdrawn" {
			return fmt.Errorf("outcome %s", m.State)
		}
		return e
	})
	// These calls bypass the new server lock and model a delayed independent
	// runtime writer retaining the original activation gate/configuration.
	if _, err = lateController.ReplaceActivationTarget(ctx, lateGate, lateTarget); !errors.Is(err, ingress.ErrActivationSuperseded) {
		t.Fatalf("late independent activation was not fenced: %v", err)
	}
	badConfig, err := appconfig.Parse([]byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	if err = lateController.PrepareCandidateDeployment(ctx, badConfig, "", strings.Repeat("c", 64), ingress.CandidateRegistrySecretName(badConfig)); err == nil {
		t.Fatal("late writer revived retired candidate")
	}
	t.Log("independent delayed activation and create rejected")
	if _, err = service.AdvanceDeployRequest(ctx, lookup("c")); err == nil {
		t.Fatal("withdrawn update advanced")
	}
	if state, err := controller.Status(ctx, "candidate-check"); err != nil || state != ingress.StatusHealthy {
		t.Fatalf("predecessor health: %s %v", state, err)
	}
	if domain != "" {
		wait("HTTPS after recovery", checkHTTPS)
	}
	// A successful update must survive database/controller recreation and
	// retire the old running generation before reporting completion.
	updated := strings.Replace(spec, "cpu: 500m", "cpu: 600m", 1)
	if _, err = service.DeployApp(ctx, request("d", updated)); err != nil {
		t.Fatal("publish successful update:", err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(ctx, "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	controller, err = ingress.NewController(ingress.ControllerConfig{KubeconfigPath: kubeconfig, Namespace: ns.Name, TLS: tls})
	if err != nil {
		t.Fatal(err)
	}
	service = newService()
	wait("successful update after server state reopen", func() error {
		m, e := service.AdvanceDeployRequest(ctx, lookup("d"))
		if e == nil && m.State != "applied" {
			return fmt.Errorf("outcome %s", m.State)
		}
		return e
	})
	previousConfig, err := appconfig.Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if retired, err := controller.CandidateRetired(ctx, previousConfig, "", strings.Repeat("b", 64), ingress.CandidateRegistrySecretName(previousConfig)); err != nil || !retired {
		t.Fatalf("old running generation retained: %v %v", retired, err)
	}
	if domain != "" {
		wait("HTTPS after successful update", checkHTTPS)
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
