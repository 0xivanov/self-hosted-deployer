package ingress

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestReconcileWithRegistryValidatesBeforeWrites(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	c := privatePullController(clientset)
	cfg := privatePullConfig()
	credential := privatePullCredential(strings.Repeat("a", 64))
	credential.AppName = "other-app"

	err := c.ReconcileWithRegistry(context.Background(), cfg, nil, "", &credential)
	if err == nil {
		t.Fatal("expected credential validation error")
	}
	if actions := clientset.Actions(); len(actions) != 0 {
		t.Fatalf("credential validation happened after Kubernetes writes: %#v", actions)
	}
}

func TestReconcileWithRegistryCreatesPrivatePullSecretWithoutCredentialLeak(t *testing.T) {
	clientset := fake.NewSimpleClientset(testReadyWorker())
	c := privatePullController(clientset)
	cfg := privatePullConfig()
	credential := privatePullCredential(cfg.ImagePullCredential)

	if err := c.ReconcileWithRegistry(context.Background(), cfg, nil, "", &credential); err != nil {
		t.Fatalf("reconcile private image: %v", err)
	}
	name := registrySecretName(cfg.Name, cfg.ImagePullCredential)
	secret, err := c.appSecrets.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get generated pull Secret: %v", err)
	}
	want, err := credential.DockerConfigJSON()
	if err != nil || string(secret.Data[corev1.DockerConfigJsonKey]) != string(want) {
		t.Fatalf("unexpected pull Secret data: err=%v", err)
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson || secret.Immutable == nil || !*secret.Immutable {
		t.Fatalf("pull Secret is not immutable dockerconfigjson: %#v", secret)
	}
	deployment, err := c.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	refs := deployment.Spec.Template.Spec.ImagePullSecrets
	if len(refs) != 1 || refs[0].Name != name {
		t.Fatalf("unexpected image pull refs: %#v", refs)
	}
	for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if env.Value == credential.Password {
			t.Fatal("credential password leaked into pod environment")
		}
	}
}

func TestReconcileWithRegistryRotationRetainsOldRevisionAndPublicSwitch(t *testing.T) {
	clientset := fake.NewSimpleClientset(testReadyWorker())
	c := privatePullController(clientset)
	cfg := privatePullConfig()
	first := strings.Repeat("1", 64)
	second := strings.Repeat("2", 64)

	cfg.ImagePullCredential = first
	firstCredential := privatePullCredential(first)
	if err := c.ReconcileWithRegistry(context.Background(), cfg, nil, "", &firstCredential); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	cfg.ImagePullCredential = second
	secondCredential := privatePullCredential(second)
	if err := c.ReconcileWithRegistry(context.Background(), cfg, nil, "", &secondCredential); err != nil {
		t.Fatalf("rotation reconcile: %v", err)
	}
	secrets, err := c.appSecrets.List(context.Background(), metav1.ListOptions{LabelSelector: appOwnershipLabel + "=" + cfg.Name + "," + registrySecretLabel + "=true"})
	if err != nil || len(secrets.Items) != 2 {
		t.Fatalf("expected both revisions retained, got %d: %v", len(secrets.Items), err)
	}
	cfg.ImagePullCredential = ""
	if err := c.ReconcileWithRegistry(context.Background(), cfg, nil, "", nil); err != nil {
		t.Fatalf("public switch: %v", err)
	}
	deployment, err := c.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if err != nil || len(deployment.Spec.Template.Spec.ImagePullSecrets) != 0 {
		t.Fatalf("public switch retained pull ref: %#v, %v", deployment.Spec.Template.Spec.ImagePullSecrets, err)
	}
	secrets, _ = c.appSecrets.List(context.Background(), metav1.ListOptions{LabelSelector: appOwnershipLabel + "=" + cfg.Name + "," + registrySecretLabel + "=true"})
	if len(secrets.Items) != 2 {
		t.Fatalf("public switch removed retained revisions: %d", len(secrets.Items))
	}
}

func TestReconcileWithRegistryRefusesForeignCollision(t *testing.T) {
	cfg := privatePullConfig()
	name := registrySecretName(cfg.Name, cfg.ImagePullCredential)
	original := []byte("foreign")
	clientset := fake.NewSimpleClientset(testReadyWorker(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: DefaultNamespace, Labels: managedAppLabels(cfg.Name)},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: original},
	})
	c := privatePullController(clientset)
	credential := privatePullCredential(cfg.ImagePullCredential)
	if err := c.ReconcileWithRegistry(context.Background(), cfg, nil, "", &credential); err == nil {
		t.Fatal("expected generated Secret collision refusal")
	}
	got, err := c.appSecrets.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil || string(got.Data[corev1.DockerConfigJsonKey]) != string(original) {
		t.Fatalf("collision Secret was changed: %#v, %v", got, err)
	}
}

func TestDeleteRemovesOnlyOwnedRegistrySecrets(t *testing.T) {
	app := privatePullConfig()
	other := app
	other.Name = "other-app"
	owned := registrySecretForTest(app.Name, app.ImagePullCredential)
	owned.UID = types.UID("owned")
	foreign := registrySecretForTest(other.Name, app.ImagePullCredential)
	foreign.UID = types.UID("foreign")
	clientset := fake.NewSimpleClientset(owned, foreign)
	c := privatePullController(clientset)
	if err := c.Delete(context.Background(), app.Name); err != nil {
		t.Fatalf("delete app: %v", err)
	}
	if _, err := c.appSecrets.Get(context.Background(), owned.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("owned registry Secret was retained")
	}
	if _, err := c.appSecrets.Get(context.Background(), foreign.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("foreign registry Secret was deleted: %v", err)
	}
}

func privatePullController(clientset *fake.Clientset) *Controller {
	clientset.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		secret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		for _, value := range secret.Labels {
			if len(value) > 63 {
				return true, nil, apierrors.NewInvalid(schema.GroupKind{Group: "core", Kind: "Secret"}, secret.Name, nil)
			}
		}
		return false, nil, nil
	})
	return &Controller{
		namespace: DefaultNamespace, tls: TLSConfig{}.WithDefaults(),
		namespaces: clientset.CoreV1().Namespaces(), ingresses: clientset.NetworkingV1().Ingresses(DefaultNamespace),
		services: clientset.CoreV1().Services(DefaultNamespace), appSecrets: clientset.CoreV1().Secrets(DefaultNamespace),
		nodes: clientset.CoreV1().Nodes(), pdbs: clientset.PolicyV1().PodDisruptionBudgets(DefaultNamespace),
		deployments: clientset.AppsV1().Deployments(DefaultNamespace),
	}
}

func privatePullConfig() appconfig.Config {
	cfg := testAppConfig()
	cfg.Image = "docker.io/library/demo@sha256:" + strings.Repeat("b", 64)
	cfg.ImagePullCredential = strings.Repeat("a", 64)
	cfg.Routing.Domain = ""
	return cfg
}

func privatePullCredential(revision string) registryauth.Credential {
	return registryauth.Credential{AppName: "my-api", Revision: revision, Registry: "docker.io", Username: "user", Password: "password"}
}

func registrySecretForTest(appName, revision string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: registrySecretName(appName, revision), Namespace: DefaultNamespace, Labels: func() map[string]string {
		labels := managedAppLabels(appName)
		labels[registrySecretLabel] = "true"
		return labels
	}(), Annotations: map[string]string{"deployer.io/registry-revision": revision}}, Type: corev1.SecretTypeDockerConfigJson, Immutable: boolPtr(true), Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("{}")}}
}

func TestPrivatePullFailuresKeepCredentialsAndRedactAPIErrors(t *testing.T) {
	cfg := privatePullConfig()
	secret := registrySecretForTest(cfg.Name, cfg.ImagePullCredential)
	clientset := fake.NewSimpleClientset(secret)
	c := privatePullController(clientset)
	clientset.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "deployments"}, cfg.Name, nil)
	})
	if err := c.Delete(context.Background(), cfg.Name); err == nil {
		t.Fatal("reported deletion despite resource failure")
	}
	if _, err := c.appSecrets.Get(context.Background(), secret.Name, metav1.GetOptions{}); err != nil {
		t.Fatal("failed deletion removed pull credentials")
	}
	clientset = fake.NewSimpleClientset(testReadyWorker())
	c = privatePullController(clientset)
	credential := privatePullCredential(cfg.ImagePullCredential)
	clientset.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewBadRequest("upstream echoed " + credential.Password)
	})
	err := c.ReconcileWithRegistry(context.Background(), cfg, nil, "", &credential)
	if err == nil || strings.Contains(err.Error(), credential.Password) {
		t.Fatal("pull Secret failure exposed credential or returned success")
	}
}
