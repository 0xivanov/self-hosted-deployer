package ingress

import (
	"context"
	"fmt"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Controller) validateRetainedStorageRuntime(cfg appconfig.Config) error {
	if cfg.Storage == nil {
		return nil
	}
	if c.pvcs == nil || c.persistentVolumes == nil || c.deployments == nil || c.pods == nil {
		return fmt.Errorf(
			"retained storage is unsupported by this runtime: PersistentVolume, " +
				"PersistentVolumeClaim, Deployment, and Pod clients are required",
		)
	}
	return nil
}

// preflightRetainedStorage verifies the externally managed claim before any
// application resource is changed. Launchstead deliberately does not create,
// resize, rebind, or delete this claim.
func (c *Controller) preflightRetainedStorage(ctx context.Context, cfg appconfig.Config) error {
	if cfg.Storage == nil {
		return nil
	}
	if err := c.validateRetainedStorageRuntime(cfg); err != nil {
		return err
	}
	claim, err := c.pvcs.Get(ctx, cfg.Storage.ExistingClaim, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf(
			"retained PersistentVolumeClaim %q does not exist in namespace %q",
			cfg.Storage.ExistingClaim,
			c.namespace,
		)
	}
	if err != nil {
		return fmt.Errorf("get retained PersistentVolumeClaim %q: %w", cfg.Storage.ExistingClaim, err)
	}
	if claim.DeletionTimestamp != nil {
		return fmt.Errorf("retained PersistentVolumeClaim %q is being deleted", claim.Name)
	}
	if claim.Spec.VolumeMode != nil && *claim.Spec.VolumeMode != corev1.PersistentVolumeFilesystem {
		return fmt.Errorf("retained PersistentVolumeClaim %q must use filesystem volume mode", claim.Name)
	}
	if claim.Status.Phase != corev1.ClaimBound || claim.Spec.VolumeName == "" {
		return fmt.Errorf("retained PersistentVolumeClaim %q must be bound before deployment", claim.Name)
	}
	if !claimSupportsWrites(claim) {
		return fmt.Errorf("retained PersistentVolumeClaim %q must declare a writable access mode", claim.Name)
	}
	volume, err := c.persistentVolumes.Get(ctx, claim.Spec.VolumeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("persistent volume %q bound to retained claim %q does not exist", claim.Spec.VolumeName, claim.Name)
	}
	if err != nil {
		return fmt.Errorf("get PersistentVolume %q for retained claim %q: %w", claim.Spec.VolumeName, claim.Name, err)
	}
	if volume.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		return fmt.Errorf("retained PersistentVolume %q must use reclaim policy Retain", volume.Name)
	}
	if volume.Spec.Local == nil {
		return fmt.Errorf("retained PersistentVolume %q must use a local volume source", volume.Name)
	}
	pinnedNodeID := cfg.Placement.Prefer[0]["node-id"]
	if !persistentVolumePinnedToNodeID(volume, pinnedNodeID) {
		return fmt.Errorf("retained PersistentVolume %q must be pinned to node-id %q", volume.Name, pinnedNodeID)
	}
	claimRefMismatch := volume.Spec.ClaimRef != nil &&
		(volume.Spec.ClaimRef.Name != claim.Name || volume.Spec.ClaimRef.Namespace != c.namespace)
	if claimRefMismatch {
		return fmt.Errorf("retained PersistentVolume %q is bound to an unexpected claim", volume.Name)
	}

	deployments, err := c.deployments.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list Deployments using retained PersistentVolumeClaim %q: %w", claim.Name, err)
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Name == cfg.Name || !deploymentUsesClaim(deployment, claim.Name) {
			continue
		}
		return fmt.Errorf(
			"retained PersistentVolumeClaim %q is already mounted by Deployment %q",
			claim.Name,
			deployment.Name,
		)
	}
	pods, err := c.pods.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list Pods using retained PersistentVolumeClaim %q: %w", claim.Name, err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed ||
			pod.Labels["deployer.io/app"] == cfg.Name || !podUsesClaim(pod, claim.Name) {
			continue
		}
		return fmt.Errorf("retained PersistentVolumeClaim %q is already mounted by Pod %q", claim.Name, pod.Name)
	}
	return nil
}

func persistentVolumePinnedToNodeID(volume *corev1.PersistentVolume, nodeID string) bool {
	if volume.Spec.NodeAffinity == nil || volume.Spec.NodeAffinity.Required == nil ||
		len(volume.Spec.NodeAffinity.Required.NodeSelectorTerms) == 0 {
		return false
	}
	for _, term := range volume.Spec.NodeAffinity.Required.NodeSelectorTerms {
		foundExactPin := false
		for _, expression := range term.MatchExpressions {
			if expression.Key != "deployer.io/node-id" || expression.Operator != corev1.NodeSelectorOpIn {
				continue
			}
			if len(expression.Values) == 1 && expression.Values[0] == nodeID {
				foundExactPin = true
			}
		}
		if !foundExactPin {
			return false
		}
	}
	return true
}

func claimSupportsWrites(claim *corev1.PersistentVolumeClaim) bool {
	for _, mode := range claim.Spec.AccessModes {
		switch mode {
		case corev1.ReadWriteOnce, corev1.ReadWriteMany, corev1.ReadWriteOncePod:
			return true
		}
	}
	return false
}

func deploymentUsesClaim(deployment *appsv1.Deployment, claimName string) bool {
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == claimName {
			return true
		}
	}
	return false
}

func podUsesClaim(pod *corev1.Pod, claimName string) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == claimName {
			return true
		}
	}
	return false
}
