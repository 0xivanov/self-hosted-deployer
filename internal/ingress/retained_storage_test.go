package ingress

import (
	"context"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func retainedStorageTestConfig(t *testing.T) appconfig.Config {
	t.Helper()
	cfg, err := appconfig.Parse([]byte(`
name: homephotos
image: ghcr.io/example/homephotos:1.0.0
service: {port: 8080, health: {path: /health}}
routing: {}
deploy: {replicas: 1}
placement:
  arch: linux/arm64
  prefer:
    - node-id: node-home
state: {mode: stateful}
resilience: {mode: pinned}
storage: {existingClaim: homephotos-data, mountPath: /var/lib/homephotos}
`))
	if err != nil {
		t.Fatalf("parse retained storage test config: %v", err)
	}
	return cfg
}

func boundRetainedClaim() *corev1.PersistentVolumeClaim {
	filesystem := corev1.PersistentVolumeFilesystem
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "homephotos-data", Namespace: DefaultNamespace},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			VolumeMode:  &filesystem,
			VolumeName:  "homephotos-pv",
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
}

func retainedLocalVolume() *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "homephotos-pv"},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				Local: &corev1.LocalVolumeSource{Path: "/srv/homephotos"},
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			ClaimRef: &corev1.ObjectReference{
				Name:      "homephotos-data",
				Namespace: DefaultNamespace,
			},
			NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key:      "deployer.io/node-id",
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"node-home"},
				}}}},
			}},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
}

func TestDeploymentForAppMountsRetainedClaimWithRecreateStrategy(t *testing.T) {
	cfg := retainedStorageTestConfig(t)
	deployment, err := deploymentForApp(cfg, DefaultNamespace, "")
	if err != nil {
		t.Fatalf("render retained storage Deployment: %v", err)
	}
	usesRecreate := deployment.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType
	if !usesRecreate || deployment.Spec.Strategy.RollingUpdate != nil {
		t.Fatalf("retained storage must use Recreate: %#v", deployment.Spec.Strategy)
	}
	if deployment.Annotations[retainedStorageClaimAnnotation] != cfg.Storage.ExistingClaim {
		t.Fatalf("missing retained storage annotation: %#v", deployment.Annotations)
	}
	if deployment.Spec.Template.Spec.NodeSelector["deployer.io/node-id"] != "node-home" {
		t.Fatalf("missing exact node selector: %#v", deployment.Spec.Template.Spec.NodeSelector)
	}
	if len(deployment.Spec.Template.Spec.Volumes) != 1 ||
		deployment.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim == nil ||
		deployment.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != cfg.Storage.ExistingClaim {
		t.Fatalf("unexpected retained storage volume: %#v", deployment.Spec.Template.Spec.Volumes)
	}
	mounts := deployment.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(mounts) != 1 || mounts[0].Name != retainedStorageVolumeName || mounts[0].MountPath != cfg.Storage.MountPath {
		t.Fatalf("unexpected retained storage mount: %#v", mounts)
	}
}

func TestPreflightRetainedStorageRequiresExclusiveBoundWritableClaim(t *testing.T) {
	cfg := retainedStorageTestConfig(t)
	tests := []struct {
		name   string
		mutate func(*corev1.PersistentVolumeClaim)
		want   string
	}{
		{name: "bound claim"},
		{name: "missing claim", want: "does not exist"},
		{
			name: "unbound claim",
			mutate: func(claim *corev1.PersistentVolumeClaim) {
				claim.Status.Phase = corev1.ClaimPending
			},
			want: "must be bound",
		},
		{name: "read-only claim", mutate: func(claim *corev1.PersistentVolumeClaim) {
			claim.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}
		}, want: "writable access mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := boundRetainedClaim()
			if tt.mutate != nil {
				tt.mutate(claim)
			}
			clientset := fake.NewSimpleClientset(retainedLocalVolume())
			if tt.name != "missing claim" {
				clientset = fake.NewSimpleClientset(claim, retainedLocalVolume())
			}
			controller := &Controller{
				namespace:         DefaultNamespace,
				pvcs:              clientset.CoreV1().PersistentVolumeClaims(DefaultNamespace),
				persistentVolumes: clientset.CoreV1().PersistentVolumes(),
				deployments:       clientset.AppsV1().Deployments(DefaultNamespace),
				pods:              clientset.CoreV1().Pods(DefaultNamespace),
			}
			err := controller.preflightRetainedStorage(context.Background(), cfg)
			if tt.want == "" && err != nil {
				t.Fatalf("preflight bound claim: %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}

	claim := boundRetainedClaim()
	other := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: DefaultNamespace},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
			Name: "data",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: claim.Name,
			}},
		}}}}},
	}
	clientset := fake.NewSimpleClientset(claim, retainedLocalVolume(), other)
	controller := &Controller{
		namespace:         DefaultNamespace,
		pvcs:              clientset.CoreV1().PersistentVolumeClaims(DefaultNamespace),
		persistentVolumes: clientset.CoreV1().PersistentVolumes(),
		deployments:       clientset.AppsV1().Deployments(DefaultNamespace),
		pods:              clientset.CoreV1().Pods(DefaultNamespace),
	}
	err := controller.preflightRetainedStorage(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "already mounted") {
		t.Fatalf("expected exclusive claim rejection, got %v", err)
	}

	foreignPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "foreign-writer", Namespace: DefaultNamespace},
		Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
			Name: "data",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: claim.Name,
			}},
		}}},
	}
	clientset = fake.NewSimpleClientset(claim, retainedLocalVolume(), foreignPod)
	controller = &Controller{
		namespace:         DefaultNamespace,
		pvcs:              clientset.CoreV1().PersistentVolumeClaims(DefaultNamespace),
		persistentVolumes: clientset.CoreV1().PersistentVolumes(),
		deployments:       clientset.AppsV1().Deployments(DefaultNamespace),
		pods:              clientset.CoreV1().Pods(DefaultNamespace),
	}
	err = controller.preflightRetainedStorage(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "Pod \"foreign-writer\"") {
		t.Fatalf("expected foreign Pod claim rejection, got %v", err)
	}
}

func TestPreflightRetainedStorageRequiresRetainedLocalVolumeOnPinnedNode(t *testing.T) {
	cfg := retainedStorageTestConfig(t)
	tests := []struct {
		name   string
		mutate func(*corev1.PersistentVolume)
		want   string
	}{
		{
			name: "delete reclaim policy",
			mutate: func(volume *corev1.PersistentVolume) {
				volume.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete
			},
			want: "reclaim policy Retain",
		},
		{
			name: "non-local source",
			mutate: func(volume *corev1.PersistentVolume) {
				volume.Spec.Local = nil
				volume.Spec.HostPath = &corev1.HostPathVolumeSource{Path: "/srv/homephotos"}
			},
			want: "local volume source",
		},
		{
			name: "wrong node",
			mutate: func(volume *corev1.PersistentVolume) {
				volume.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values = []string{"node-other"}
			},
			want: `pinned to node-id "node-home"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volume := retainedLocalVolume()
			tt.mutate(volume)
			clientset := fake.NewSimpleClientset(boundRetainedClaim(), volume)
			controller := &Controller{
				namespace:         DefaultNamespace,
				pvcs:              clientset.CoreV1().PersistentVolumeClaims(DefaultNamespace),
				persistentVolumes: clientset.CoreV1().PersistentVolumes(),
				deployments:       clientset.AppsV1().Deployments(DefaultNamespace),
				pods:              clientset.CoreV1().Pods(DefaultNamespace),
			}
			err := controller.preflightRetainedStorage(context.Background(), cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestDeleteAppRetainsExternallyManagedClaim(t *testing.T) {
	cfg := retainedStorageTestConfig(t)
	claim := boundRetainedClaim()
	deployment, err := deploymentForApp(cfg, DefaultNamespace, "")
	if err != nil {
		t.Fatal(err)
	}
	volume := retainedLocalVolume()
	clientset := fake.NewSimpleClientset(claim, volume, deployment, serviceForApp(cfg, DefaultNamespace))
	controller := &Controller{
		namespace:         DefaultNamespace,
		ingresses:         clientset.NetworkingV1().Ingresses(DefaultNamespace),
		services:          clientset.CoreV1().Services(DefaultNamespace),
		networkPolicies:   clientset.NetworkingV1().NetworkPolicies(DefaultNamespace),
		appSecrets:        clientset.CoreV1().Secrets(DefaultNamespace),
		pvcs:              clientset.CoreV1().PersistentVolumeClaims(DefaultNamespace),
		persistentVolumes: clientset.CoreV1().PersistentVolumes(),
		pdbs:              clientset.PolicyV1().PodDisruptionBudgets(DefaultNamespace),
		deployments:       clientset.AppsV1().Deployments(DefaultNamespace),
	}
	if err := controller.Delete(context.Background(), cfg.Name); err != nil {
		t.Fatalf("delete retained storage app: %v", err)
	}
	if _, err := controller.pvcs.Get(context.Background(), claim.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("app deletion must retain existing PVC: %v", err)
	}
	_, err = controller.deployments.Get(context.Background(), cfg.Name, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("application Deployment was not deleted: %v", err)
	}
	err = controller.ValidateNodeRemoval(
		context.Background(),
		domain.Node{ID: "node-home", Name: "pi-home"},
	)
	if err == nil || !strings.Contains(err.Error(), volume.Name) {
		t.Fatalf("retained local PV must continue guarding its node after app deletion: %v", err)
	}
}

func TestNodeRemovalGuardBlocksRetainedStorageNode(t *testing.T) {
	cfg := retainedStorageTestConfig(t)
	deployment, err := deploymentForApp(cfg, DefaultNamespace, "")
	if err != nil {
		t.Fatal(err)
	}
	clientset := fake.NewSimpleClientset(deployment)
	controller := &Controller{deployments: clientset.AppsV1().Deployments(DefaultNamespace)}
	err = controller.ValidateNodeRemoval(context.Background(), domain.Node{ID: "node-home", Name: "pi-home"})
	if err == nil || !strings.Contains(err.Error(), "homephotos-data") {
		t.Fatalf("expected retained storage node removal guard, got %v", err)
	}
	err = controller.ValidateNodeRemoval(
		context.Background(),
		domain.Node{ID: "node-other", Name: "pi-other"},
	)
	if err != nil {
		t.Fatalf("unrelated node removal was blocked: %v", err)
	}
}
