package ingress

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestCandidateDeletionFencesAndFinishesOwnedService(t *testing.T) {
	cfg := hostingTestConfig(t)
	client := fake.NewSimpleClientset()
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), pods: client.CoreV1().Pods(DefaultNamespace)}
	service := serviceForApp(cfg, DefaultNamespace)
	service.UID = "service-delete"
	service.ResourceVersion = "1"
	if _, err := c.services.Create(context.Background(), service, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	gate, err := c.BeginCandidateDeletion(context.Background(), cfg, "app-delete", false)
	if err != nil {
		t.Fatal(err)
	}
	if gate.OperationID != candidateDeletionOperationID(cfg.Name, "app-delete") {
		t.Fatalf("unexpected deletion operation: %q", gate.OperationID)
	}
	if err := c.CheckCandidateDeletion(context.Background(), cfg.Name); !errors.Is(err, ErrCandidateDeletionInProgress) {
		t.Fatalf("deletion marker was not visible: %v", err)
	}
	replayed, err := c.BeginCandidateDeletion(context.Background(), cfg, "app-delete", false)
	if err != nil || replayed != gate {
		t.Fatalf("deletion replay changed gate: before=%+v after=%+v err=%v", gate, replayed, err)
	}
	if err := c.FinishCandidateDeletion(context.Background(), gate, "app-delete"); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckCandidateDeletion(context.Background(), cfg.Name); err != nil {
		t.Fatalf("finished deletion still blocked: %v", err)
	}
}

func TestCheckCandidateDeletionRejectsMalformedMarkerPresence(t *testing.T) {
	cfg := hostingTestConfig(t)
	client := fake.NewSimpleClientset()
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace)}
	service := serviceForApp(cfg, DefaultNamespace)
	service.Annotations[deletionOperationAnnotation] = ""
	if _, err := c.services.Create(context.Background(), service, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckCandidateDeletion(context.Background(), cfg.Name); !errors.Is(err, ErrCandidateDeletionInProgress) {
		t.Fatalf("empty deletion marker was ignored: %v", err)
	}
}

func TestCandidateDeletionReplaysAfterLostAtomicBeginResponse(t *testing.T) {
	cfg := hostingTestConfig(t)
	client := fake.NewSimpleClientset()
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace)}
	service := serviceForApp(cfg, DefaultNamespace)
	service.UID = "service-lost"
	service.ResourceVersion = "1"
	if _, err := c.services.Create(context.Background(), service, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	updates := 0
	client.PrependReactor("update", "services", func(action ktesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 1 {
			update := action.(ktesting.UpdateAction).GetObject()
			if err := client.Tracker().Update(schema.GroupVersionResource{Version: "v1", Resource: "services"}, update, DefaultNamespace); err != nil {
				return true, nil, err
			}
			return true, update, errors.New("lost begin response")
		}
		return false, nil, nil
	})
	if _, err := c.BeginCandidateDeletion(context.Background(), cfg, "app-lost", false); err == nil {
		t.Fatal("lost Begin response was reported as success")
	}
	gate, err := c.BeginCandidateDeletion(context.Background(), cfg, "app-lost", false)
	if err != nil || gate.OperationID != candidateDeletionOperationID(cfg.Name, "app-lost") || updates != 1 {
		t.Fatalf("atomic Begin was not replayable: gate=%+v err=%v updates=%d", gate, err, updates)
	}
}

func TestCandidateDeletionCleanupWaitsAndPreservesOtherApps(t *testing.T) {
	cfg := hostingTestConfig(t)
	client := fake.NewSimpleClientset()
	c := &Controller{
		namespace:       DefaultNamespace,
		services:        client.CoreV1().Services(DefaultNamespace),
		pods:            client.CoreV1().Pods(DefaultNamespace),
		deployments:     client.AppsV1().Deployments(DefaultNamespace),
		ingresses:       client.NetworkingV1().Ingresses(DefaultNamespace),
		networkPolicies: client.NetworkingV1().NetworkPolicies(DefaultNamespace),
		pdbs:            client.PolicyV1().PodDisruptionBudgets(DefaultNamespace),
		appSecrets:      client.CoreV1().Secrets(DefaultNamespace),
	}
	service := serviceForApp(cfg, DefaultNamespace)
	service.UID = "service-cleanup"
	service.ResourceVersion = "1"
	if _, err := c.services.Create(context.Background(), service, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	gate, err := c.BeginCandidateDeletion(context.Background(), cfg, "app-cleanup", false)
	if err != nil {
		t.Fatal(err)
	}
	replicas := int32(2)
	legacy, err := deploymentForApp(cfg, DefaultNamespace, "")
	if err != nil {
		t.Fatal(err)
	}
	legacy.UID = "legacy-cleanup"
	legacy.ResourceVersion = "1"
	legacy.Generation = 1
	legacy.Spec.Replicas = &replicas
	legacy.Status.Replicas = 2
	legacy.Status.UpdatedReplicas = 2
	legacy.Status.ReadyReplicas = 2
	legacy.Status.AvailableReplicas = 2
	if _, err := c.deployments.Create(context.Background(), legacy, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	candidate, err := CandidateDeploymentForApp(cfg, DefaultNamespace, "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	if err != nil {
		t.Fatal(err)
	}
	candidate.UID = "candidate-tombstone"
	candidate.ResourceVersion = "1"
	candidate.Spec.Replicas = int32Ptr(0)
	candidate.Generation = 1
	candidate.Status.ObservedGeneration = 1
	if _, err := c.deployments.Create(context.Background(), candidate, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	other, err := deploymentForApp(appconfig.Config{Name: "other-app", Image: cfg.Image, Service: cfg.Service, State: cfg.State, Deploy: cfg.Deploy}, DefaultNamespace, "")
	if err != nil {
		t.Fatal(err)
	}
	other.UID = "other"
	other.ResourceVersion = "1"
	if _, err := c.deployments.Create(context.Background(), other, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "legacy-pod", Namespace: DefaultNamespace, Labels: map[string]string{appOwnershipLabel: cfg.Name}}}
	if _, err := c.pods.Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: DefaultNamespace, Labels: managedAppLabels(cfg.Name)}}
	if _, err := c.ingresses.Create(context.Background(), ingress, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: DefaultNamespace, Labels: managedAppLabels(cfg.Name)}}
	if _, err := c.appSecrets.Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if ready, err := c.CleanupCandidateDeletion(context.Background(), gate, "app-cleanup"); err != nil || ready {
		t.Fatalf("cleanup ignored running app pod: ready=%v err=%v", ready, err)
	}
	legacy, err = c.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	legacy.Status.ObservedGeneration = legacy.Generation
	legacy.Status.Replicas = 0
	legacy.Status.UpdatedReplicas = 0
	legacy.Status.ReadyReplicas = 0
	legacy.Status.AvailableReplicas = 0
	if _, err := c.deployments.UpdateStatus(context.Background(), legacy, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if ready, err := c.CleanupCandidateDeletion(context.Background(), gate, "app-cleanup"); err != nil || ready {
		t.Fatalf("cleanup ignored pod after Deployment reported zero replicas: ready=%v err=%v", ready, err)
	}
	if err := c.pods.Delete(context.Background(), pod.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	unknown := candidate.DeepCopy()
	unknown.Name = "unrecorded-candidate"
	unknown.UID = "unrecorded-candidate-uid"
	unknown.Spec.Replicas = int32Ptr(1)
	if _, err := c.deployments.Create(context.Background(), unknown, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if ready, err := c.CleanupCandidateDeletion(context.Background(), gate, "app-cleanup"); err != nil || ready {
		t.Fatalf("unrecorded workload allowed cleanup: ready=%v err=%v", ready, err)
	}
	if _, err := c.appSecrets.Get(context.Background(), cfg.Name, metav1.GetOptions{}); err != nil {
		t.Fatal("secret removed before all workloads drained")
	}
	unknown.Spec.Replicas = int32Ptr(0)
	if _, err := c.deployments.Update(context.Background(), unknown, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	ready, err := c.CleanupCandidateDeletion(context.Background(), gate, "app-cleanup")
	if err != nil || !ready {
		t.Fatalf("cleanup did not finish after drain: ready=%v err=%v", ready, err)
	}
	if _, err := c.deployments.Get(context.Background(), candidate.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("candidate tombstone was removed: %v", err)
	}
	if unchanged, err := c.deployments.Get(context.Background(), other.Name, metav1.GetOptions{}); err != nil || !reflect.DeepEqual(unchanged, other) {
		t.Fatalf("other app was touched: %v", err)
	}
	if _, err := c.ingresses.Get(context.Background(), cfg.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("owned ingress was not removed: %v", err)
	}
	if _, err := c.appSecrets.Get(context.Background(), cfg.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("owned secret was not removed: %v", err)
	}
}

func TestCandidateDeletionAllowsMissingServiceOnlyForDeletedAppReplay(t *testing.T) {
	cfg := hostingTestConfig(t)
	client := fake.NewSimpleClientset()
	c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), pods: client.CoreV1().Pods(DefaultNamespace)}
	client.PrependReactor("create", "services", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		service := create.GetObject().(*corev1.Service).DeepCopy()
		service.UID = "service-missing-replay"
		service.ResourceVersion = "1"
		if err := client.Tracker().Create(schema.GroupVersionResource{Version: "v1", Resource: "services"}, service, DefaultNamespace); err != nil {
			return true, nil, err
		}
		return true, service, nil
	})
	gate, err := c.BeginCandidateDeletion(context.Background(), cfg, "app-deleted", true)
	if err != nil || gate.UID != "service-missing-replay" {
		t.Fatalf("missing-service deletion bootstrap failed: gate=%+v err=%v", gate, err)
	}
	if err := c.FinishCandidateDeletion(context.Background(), gate, "app-deleted"); err != nil {
		t.Fatalf("missing-service deletion finish failed: %v", err)
	}
	if _, err := c.services.Get(context.Background(), cfg.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("deletion Service remained after finish: %v", err)
	}
	if _, err := c.BeginCandidateDeletion(context.Background(), cfg, "active-app", false); err == nil {
		t.Fatal("active app with missing Service was accepted")
	}
}
