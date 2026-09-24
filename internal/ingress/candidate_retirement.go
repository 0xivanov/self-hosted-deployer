package ingress

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const candidateRetiredAnnotation = "deployer.io/candidate-retired"

// RetireCandidateDeployment leaves an immutable, zero-replica tombstone. It
// never deletes the Deployment, policy, or referenced Secrets, so a late
// create-only writer cannot resurrect the same generation.
func (c *Controller) RetireCandidateDeployment(ctx context.Context, cfg appconfig.Config, secretRevision, requestID, registrySecretName string) error {
	if c.deployments == nil {
		return errors.New("candidate deployment runtime is unavailable")
	}
	desired, err := CandidateDeploymentForApp(cfg, c.namespace, secretRevision, requestID, registrySecretName)
	if err != nil {
		return err
	}
	retired := desired.DeepCopy()
	retired.Spec.Replicas = int32Ptr(0)
	if retired.Annotations == nil {
		retired.Annotations = map[string]string{}
	}
	retired.Annotations[candidateRetiredAnnotation] = "true"
	existing, err := c.deployments.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.deployments.Create(ctx, retired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create candidate retirement tombstone: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read candidate deployment for retirement: %w", err)
	}
	if existing.UID == "" || existing.ResourceVersion == "" || existing.DeletionTimestamp != nil {
		return errors.New("candidate deployment lacks a stable retirement identity")
	}
	if err := requireAppResourceOwnership("Deployment", existing.Name, cfg.Name, existing.Labels); err != nil {
		return err
	}
	if !candidateDeploymentMatches(existing, desired) && !candidateDeploymentMatches(existing, retired) {
		return errors.New("candidate deployment identity or content changed")
	}
	if candidateDeploymentMatches(existing, retired) {
		return nil
	}
	update := existing.DeepCopy()
	update.Spec.Replicas = int32Ptr(0)
	if update.Annotations == nil {
		update.Annotations = map[string]string{}
	}
	update.Annotations[candidateRetiredAnnotation] = "true"
	if _, err := c.deployments.Update(ctx, update, metav1.UpdateOptions{}); err != nil {
		if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
			return errors.New("candidate retirement was superseded; retry after reread")
		}
		return fmt.Errorf("retire candidate deployment: %w", err)
	}
	return nil
}

// CandidateRetired verifies the immutable tombstone, zero observed workload
// state, and absence of generation-matching Pod objects. It does not prove
// that external traffic has been switched.
func (c *Controller) CandidateRetired(ctx context.Context, cfg appconfig.Config, secretRevision, requestID, registrySecretName string) (bool, error) {
	if c.deployments == nil || c.pods == nil {
		return false, errors.New("candidate deployment runtime is unavailable")
	}
	desired, err := CandidateDeploymentForApp(cfg, c.namespace, secretRevision, requestID, registrySecretName)
	if err != nil {
		return false, err
	}
	existing, err := c.deployments.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read candidate deployment retirement: %w", err)
	}
	if existing.UID == "" || existing.ResourceVersion == "" || existing.DeletionTimestamp != nil {
		return false, nil
	}
	if err := requireAppResourceOwnership("Deployment", existing.Name, cfg.Name, existing.Labels); err != nil {
		return false, err
	}
	retired := desired.DeepCopy()
	retired.Spec.Replicas = int32Ptr(0)
	if retired.Annotations == nil {
		retired.Annotations = map[string]string{}
	}
	retired.Annotations[candidateRetiredAnnotation] = "true"
	if existing.Generation < 1 || existing.Status.ObservedGeneration < existing.Generation {
		return false, nil
	}
	if existing.Status.Replicas != 0 || existing.Status.UpdatedReplicas != 0 || existing.Status.ReadyReplicas != 0 || existing.Status.AvailableReplicas != 0 || existing.Status.UnavailableReplicas != 0 {
		return false, nil
	}
	if !candidateDeploymentMatches(existing, retired) {
		return false, nil
	}
	selector, err := CandidateSelector(cfg.Name, requestID)
	if err != nil {
		return false, err
	}
	pods, err := c.pods.List(ctx, metav1.ListOptions{LabelSelector: labels.Set(selector).AsSelector().String()})
	if err != nil {
		return false, fmt.Errorf("list candidate pods for retirement: %w", err)
	}
	return len(pods.Items) == 0, nil
}
