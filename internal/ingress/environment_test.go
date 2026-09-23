package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestEnvironmentSnapshotsPublishRotateRestoreAndClear(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewSimpleClientset(testReadyWorker())
	c := privatePullController(clientset)
	cfg := privatePullConfig()
	credential := privatePullCredential(cfg.ImagePullCredential)
	first, second, empty := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)
	for _, step := range []struct {
		id, value string
		clear     bool
	}{{first, "first-synthetic", false}, {second, "second-synthetic", false}, {first, "first-synthetic", false}, {empty, "", true}} {
		cfg.EnvironmentRevision = step.id
		values := map[string]string{}
		if !step.clear {
			values["TOKEN"] = step.value
			values["EMPTY"] = ""
		}
		if err := c.ReconcileWithRegistry(ctx, cfg, values, step.id, &credential); err != nil {
			t.Fatal(err)
		}
		secret, err := c.appSecrets.Get(ctx, environmentSecretName(cfg.Name, step.id), metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if secret.Type != corev1.SecretTypeOpaque || secret.Immutable == nil || !*secret.Immutable || len(secret.Data) != len(values) || string(secret.Data["TOKEN"]) != step.value {
			t.Fatal("wrong immutable environment snapshot")
		}
		deployment, err := c.deployments.Get(ctx, cfg.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		container := deployment.Spec.Template.Spec.Containers[0]
		if len(container.EnvFrom) != 1 || container.EnvFrom[0].SecretRef.Name != secret.Name || deployment.Spec.Template.Annotations[secretHashAnnotation] != step.id {
			t.Fatal("deployment missing environment reference")
		}
		if len(deployment.Spec.Template.Spec.ImagePullSecrets) != 1 {
			t.Fatal("lost private registry reference")
		}
		raw, _ := json.Marshal(deployment)
		if strings.Contains(string(raw), "synthetic") {
			t.Fatal("values exposed in deployment")
		}
	}
	cfg.EnvironmentRevision = ""
	if err := c.ReconcileWithRegistry(ctx, cfg, nil, "", &credential); err != nil {
		t.Fatal(err)
	}
	deployment, _ := c.deployments.Get(ctx, cfg.Name, metav1.GetOptions{})
	if len(deployment.Spec.Template.Spec.Containers[0].EnvFrom) != 0 {
		t.Fatal("environment not cleared")
	}
	for _, id := range []string{first, second, empty} {
		if _, err := c.appSecrets.Get(ctx, environmentSecretName(cfg.Name, id), metav1.GetOptions{}); err != nil {
			t.Fatal("restorable snapshot deleted")
		}
	}
	if err := c.Delete(ctx, cfg.Name); err != nil {
		t.Fatal(err)
	}
	secrets, err := c.appSecrets.List(ctx, metav1.ListOptions{})
	if err != nil || len(secrets.Items) != 0 {
		t.Fatal("environment snapshots not cleaned")
	}
}

func TestEnvironmentRejectsRevisionOrDataMismatch(t *testing.T) {
	clientset := fake.NewSimpleClientset(testReadyWorker())
	c := privatePullController(clientset)
	cfg := testAppConfig()
	cfg.EnvironmentRevision = strings.Repeat("a", 64)
	if err := c.Reconcile(context.Background(), cfg, map[string]string{"TOKEN": "x"}, "wrong"); err == nil || len(clientset.Actions()) != 0 {
		t.Fatal("invalid revision reached Kubernetes")
	}
	cfg.Routing.Domain = ""
	if err := c.Reconcile(context.Background(), cfg, map[string]string{"EMPTY": ""}, cfg.EnvironmentRevision); err != nil {
		t.Fatal(err)
	}
	if err := c.Reconcile(context.Background(), cfg, map[string]string{"DIFFERENT": ""}, cfg.EnvironmentRevision); err == nil {
		t.Fatal("immutable keys replaced")
	}
}

func TestEnvironmentSecretErrorsDoNotExposeValues(t *testing.T) {
	clientset := fake.NewSimpleClientset(testReadyWorker())
	c := privatePullController(clientset)
	cfg := testAppConfig()
	cfg.EnvironmentRevision = strings.Repeat("a", 64)
	clientset.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("synthetic-sensitive-value")
	})
	err := c.reconcileEnvironmentSecret(context.Background(), cfg, map[string]string{"TOKEN": "synthetic-sensitive-value"})
	if err == nil || strings.Contains(err.Error(), "synthetic-sensitive-value") {
		t.Fatal("raw Secret error exposed")
	}
}
