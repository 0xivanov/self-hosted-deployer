package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	deletionOperationAnnotation = "deployer.io/deletion-operation"
	deletingAppIDAnnotation     = "deployer.io/deleting-app-id"
	legacyDeletionAnnotation    = "deployer.io/deletion-tombstone"
)

var ErrCandidateDeletionInProgress = errors.New("app deletion is in progress")

func candidateDeletionOperationID(app, appID string) string {
	sum := sha256.Sum256([]byte("candidate-deletion-v1\x00" + app + "\x00" + appID))
	return hex.EncodeToString(sum[:])
}

func deletionTarget(cfg appconfig.Config, operationID, namespace string) (ActivationTarget, error) {
	if !registryauth.ValidRevision(operationID) {
		return ActivationTarget{}, errors.New("invalid deletion operation identity")
	}
	return ActivationTarget{
		Selector: map[string]string{appOwnershipLabel: cfg.Name, candidateGenerationLabel: "inactive-" + candidateGeneration(cfg.Name, operationID)},
		Ports:    serviceForApp(cfg, namespace).Spec.Ports,
	}, nil
}

// CheckCandidateDeletion reports the durable Service marker used to block new
// work while an app is being removed. A missing Service is not a deletion
// marker and is therefore allowed to be handled by the caller's app state.
func (c *Controller) CheckCandidateDeletion(ctx context.Context, app string) error {
	if c.services == nil {
		return errors.New("Service client required for deletion guard")
	}
	service, err := c.services.Get(ctx, strings.TrimSpace(app), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, hasOperation := service.Annotations[deletionOperationAnnotation]; hasOperation {
		return ErrCandidateDeletionInProgress
	}
	if _, hasAppID := service.Annotations[deletingAppIDAnnotation]; hasAppID {
		return ErrCandidateDeletionInProgress
	}
	return nil
}

// BeginCandidateDeletion fences the stable Service and changes it to an
// explicit inactive selector. The Service remains as a durable tombstone
// until FinishCandidateDeletion removes it after all workloads are gone.
func (c *Controller) BeginCandidateDeletion(ctx context.Context, cfg appconfig.Config, appID string, allowMissing bool) (ActivationGate, error) {
	if c.services == nil || strings.TrimSpace(appID) == "" || cfg.Validate() != nil || cfg.Name == "" {
		return ActivationGate{}, errors.New("invalid candidate deletion configuration")
	}
	operationID := candidateDeletionOperationID(cfg.Name, appID)
	target, err := deletionTarget(cfg, operationID, c.namespace)
	if err != nil {
		return ActivationGate{}, err
	}
	service, err := c.services.Get(ctx, cfg.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if !allowMissing {
			return ActivationGate{}, errors.New("app Service is missing before deletion")
		}
		service = serviceForApp(cfg, c.namespace)
		service.Spec.Selector = maps.Clone(target.Selector)
		service.Annotations[deletionOperationAnnotation] = operationID
		service.Annotations[deletingAppIDAnnotation] = appID
		service.Annotations[activationOperationAnnotation] = operationID
		created, createErr := c.services.Create(ctx, service, metav1.CreateOptions{})
		if createErr != nil {
			if !apierrors.IsAlreadyExists(createErr) {
				return ActivationGate{}, createErr
			}
			service, err = c.services.Get(ctx, cfg.Name, metav1.GetOptions{})
			if err != nil {
				return ActivationGate{}, err
			}
		} else {
			service = created
		}
	} else if err != nil {
		return ActivationGate{}, err
	}
	if service.DeletionTimestamp != nil {
		return ActivationGate{}, errors.New("app Service is being deleted")
	}
	if err := requireAppResourceOwnership("Service", service.Name, cfg.Name, service.Labels); err != nil {
		return ActivationGate{}, err
	}
	_, hasOperation := service.Annotations[deletionOperationAnnotation]
	_, hasAppID := service.Annotations[deletingAppIDAnnotation]
	if hasOperation || hasAppID {
		if service.Annotations[deletionOperationAnnotation] != operationID || service.Annotations[deletingAppIDAnnotation] != appID || service.Annotations[activationOperationAnnotation] != operationID {
			return ActivationGate{}, ErrCandidateDeletionInProgress
		}
		if service.UID == "" || service.ResourceVersion == "" {
			return ActivationGate{}, errors.New("deletion Service lacks stable identity")
		}
		if service.DeletionTimestamp != nil || !maps.Equal(service.Spec.Selector, target.Selector) || !apiequality.Semantic.DeepEqual(service.Spec.Ports, target.Ports) {
			return ActivationGate{}, errors.New("deletion Service marker has an unexpected selector")
		}
		return activationGateFor(service), nil
	}
	gate := activationGateFor(service)
	serviceGate, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return ActivationGate{}, err
	}
	if serviceGate.Annotations == nil {
		serviceGate.Annotations = map[string]string{}
	}
	serviceGate.Annotations[activationOperationAnnotation] = operationID
	serviceGate.Annotations[deletionOperationAnnotation] = operationID
	serviceGate.Annotations[deletingAppIDAnnotation] = appID
	serviceGate.Spec.Selector = maps.Clone(target.Selector)
	serviceGate.Spec.Ports = append([]corev1.ServicePort(nil), target.Ports...)
	updated, err := c.updateActivationGate(ctx, serviceGate)
	if err != nil {
		return ActivationGate{}, err
	}
	return updated, nil
}

func (c *Controller) deletionServiceAtGate(ctx context.Context, gate ActivationGate, appID string) (*corev1.Service, error) {
	if gate.App == "" || appID == "" || gate.OperationID != candidateDeletionOperationID(gate.App, appID) {
		return nil, ErrActivationSuperseded
	}
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return nil, err
	}
	expectedSelector := map[string]string{appOwnershipLabel: gate.App, candidateGenerationLabel: "inactive-" + candidateGeneration(gate.App, gate.OperationID)}
	if service.DeletionTimestamp != nil || service.UID == "" || service.ResourceVersion == "" || service.Annotations[deletionOperationAnnotation] != gate.OperationID || service.Annotations[deletingAppIDAnnotation] != appID || service.Annotations[activationOperationAnnotation] != gate.OperationID || !maps.Equal(service.Spec.Selector, expectedSelector) {
		return nil, ErrActivationSuperseded
	}
	return service, nil
}

// retireLegacyCandidatePredecessor leaves the legacy app Deployment as a
// zero-replica tombstone and waits for its observed drain. It is shared by
// deletion and candidate cutover; neither path removes the Deployment here.
func (c *Controller) retireLegacyCandidatePredecessor(ctx context.Context, gate ActivationGate) (bool, error) {
	if c.deployments == nil || c.pods == nil {
		return false, errors.New("legacy predecessor retirement requires Deployment and Pod clients")
	}
	legacy, err := c.deployments.Get(ctx, gate.App, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		legacy = nil
	} else if err != nil {
		return false, err
	}
	if legacy != nil {
		if err = requireAppResourceOwnership("Deployment", legacy.Name, gate.App, legacy.Labels); err != nil {
			return false, err
		}
		if legacy.UID == "" || legacy.ResourceVersion == "" || legacy.DeletionTimestamp != nil {
			return false, errors.New("legacy app Deployment lacks stable retirement identity")
		}
		if legacy.Annotations == nil {
			legacy.Annotations = map[string]string{}
		}
		needsUpdate := legacy.Annotations[legacyDeletionAnnotation] != gate.OperationID || legacy.Spec.Replicas == nil || *legacy.Spec.Replicas != 0
		if needsUpdate {
			legacy.Spec.Replicas = int32Ptr(0)
			legacy.Annotations[legacyDeletionAnnotation] = gate.OperationID
			if _, err = c.deployments.Update(ctx, legacy, metav1.UpdateOptions{}); apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
				return false, ErrActivationSuperseded
			} else if err != nil {
				return false, err
			}
		}
		legacy, err = c.deployments.Get(ctx, gate.App, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if legacy.Annotations[legacyDeletionAnnotation] != gate.OperationID || legacy.Generation < 1 || legacy.Status.ObservedGeneration < legacy.Generation || legacy.Spec.Replicas == nil || *legacy.Spec.Replicas != 0 || legacy.Status.Replicas != 0 || legacy.Status.UpdatedReplicas != 0 || legacy.Status.ReadyReplicas != 0 || legacy.Status.AvailableReplicas != 0 || legacy.Status.UnavailableReplicas != 0 {
			return false, nil
		}
	}
	legacyPods, err := c.pods.List(ctx, metav1.ListOptions{LabelSelector: labels.Set{"app.kubernetes.io/name": gate.App}.AsSelector().String()})
	if err != nil {
		return false, err
	}
	for i := range legacyPods.Items {
		pod := &legacyPods.Items[i]
		if err = requireAppResourceOwnership("Pod", pod.Name, gate.App, pod.Labels); err != nil {
			return false, err
		}
	}
	return len(legacyPods.Items) == 0, nil
}

// RetireLegacyCandidatePredecessor drains only the legacy predecessor while
// the Service selects the active candidate generation. It never touches the
// selected candidate, routes, secrets, or Service.
func (c *Controller) RetireLegacyCandidatePredecessor(ctx context.Context, gate ActivationGate) (bool, error) {
	if c.services == nil || !registryauth.ValidRevision(gate.OperationID) {
		return false, errors.New("candidate predecessor retirement is unavailable")
	}
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return false, err
	}
	if service.DeletionTimestamp != nil {
		return false, ErrActivationSuperseded
	}
	expected, err := CandidateSelector(gate.App, gate.OperationID)
	if err != nil || !maps.Equal(service.Spec.Selector, expected) || service.Annotations[activationOperationAnnotation] != gate.OperationID {
		return false, ErrActivationSuperseded
	}
	if _, deleting := service.Annotations[deletionOperationAnnotation]; deleting {
		return false, errors.New("candidate predecessor retirement cannot run during deletion")
	}
	if _, deleting := service.Annotations[deletingAppIDAnnotation]; deleting {
		return false, errors.New("candidate predecessor retirement cannot run during deletion")
	}
	ready, err := c.retireLegacyCandidatePredecessor(ctx, gate)
	if err != nil {
		return false, err
	}
	if _, err = c.serviceAtGate(ctx, gate); err != nil {
		return false, err
	}
	return ready, nil
}

// CleanupCandidateDeletion drains and removes owned non-candidate resources.
// Candidate tombstones remain until old writers are drained by deployment
// qualification; they have zero replicas and consume no compute.
func (c *Controller) CleanupCandidateDeletion(ctx context.Context, gate ActivationGate, appID string) (bool, error) {
	if c.services == nil || c.deployments == nil || c.pods == nil || c.ingresses == nil || c.appSecrets == nil || c.networkPolicies == nil {
		return false, errors.New("candidate deletion resource clients are unavailable")
	}
	_, err := c.deletionServiceAtGate(ctx, gate, appID)
	if err != nil {
		return false, err
	}
	if ready, err := c.retireLegacyCandidatePredecessor(ctx, gate); err != nil || !ready {
		return false, err
	}
	pods, listErr := c.pods.List(ctx, metav1.ListOptions{LabelSelector: labels.Set{appOwnershipLabel: gate.App}.AsSelector().String()})
	if listErr != nil {
		return false, listErr
	}
	if len(pods.Items) != 0 {
		return false, nil
	}
	deployments, listErr := c.deployments.List(ctx, metav1.ListOptions{LabelSelector: labels.Set{appOwnershipLabel: gate.App}.AsSelector().String()})
	if listErr != nil {
		return false, listErr
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if err = requireAppResourceOwnership("Deployment", deployment.Name, gate.App, deployment.Labels); err != nil {
			return false, err
		}
		if deployment.UID == "" || deployment.ResourceVersion == "" || deployment.Generation < 1 || deployment.DeletionTimestamp != nil || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 || deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.Replicas != 0 || deployment.Status.UpdatedReplicas != 0 || deployment.Status.ReadyReplicas != 0 || deployment.Status.AvailableReplicas != 0 || deployment.Status.UnavailableReplicas != 0 {
			return false, nil
		}
	}
	var cleanupErrs []error
	cleanupErrs = append(cleanupErrs,
		c.deleteIngress(ctx, gate.App),
		c.deleteHostingNetworkPolicy(ctx, gate.App),
		c.deleteTrafficResilienceResources(ctx, gate.App),
		c.deletePodDisruptionBudget(ctx, gate.App),
		c.deleteAppSecret(ctx, gate.App),
		c.deleteRegistrySecrets(ctx, gate.App),
		c.deleteEnvironmentSecrets(ctx, gate.App),
	)
	if err = errors.Join(cleanupErrs...); err != nil {
		return false, err
	}
	if _, err = c.deletionServiceAtGate(ctx, gate, appID); err != nil {
		return false, err
	}
	return true, nil
}

// FinishCandidateDeletion removes only the fenced, owned Service. A missing
// Service is an idempotent success after the caller has completed cleanup.
func (c *Controller) FinishCandidateDeletion(ctx context.Context, gate ActivationGate, appID string) error {
	if c.services == nil || c.pods == nil {
		return errors.New("candidate deletion requires Service and Pod clients")
	}
	service, err := c.services.Get(ctx, gate.App, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	validated, err := c.deletionServiceAtGate(ctx, gate, appID)
	if err != nil {
		return err
	}
	service = validated
	pods, listErr := c.pods.List(ctx, metav1.ListOptions{LabelSelector: labels.Set{appOwnershipLabel: gate.App}.AsSelector().String()})
	if listErr != nil || len(pods.Items) != 0 {
		if listErr != nil {
			return listErr
		}
		return errors.New("app still has owned Pods")
	}
	if err = c.services.Delete(ctx, service.Name, ownedDeleteOptionsWithResourceVersion(service)); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("delete fenced app Service: %w", err)
	}
	return nil
}
