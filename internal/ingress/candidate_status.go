package ingress

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ErrCandidateStatusNotSelected tells a caller to use the legacy status path.
// It is returned when the stable Service has no valid candidate activation
// identity or the candidate is not uniquely selectable.
var ErrCandidateStatusNotSelected = errors.New("candidate status is not selected")

var candidateGenerationPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// CandidateStatus is read-only status for the Deployment selected by the
// stable Service's activation operation.
type CandidateStatus struct {
	State             string
	DesiredReplicas   int32
	AvailableReplicas int32
	DeploymentName    string
}

// CandidateStatus reads only the candidate selected by the owned stable
// Service. It never mutates Kubernetes objects and never falls back to the
// legacy Deployment implicitly.
func (c *Controller) CandidateStatus(ctx context.Context, appName string) (CandidateStatus, error) {
	var result CandidateStatus
	if strings.TrimSpace(appName) == "" || c.services == nil || c.deployments == nil {
		return result, errors.New("candidate status runtime is unavailable")
	}
	service, err := c.services.Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		return result, fmt.Errorf("get Service %q for candidate status: %w", appName, err)
	}
	if err := requireAppResourceOwnership("Service", service.Name, appName, service.Labels); err != nil {
		return result, err
	}
	operationID := strings.TrimSpace(service.Annotations[activationOperationAnnotation])
	generation, hasGeneration := service.Spec.Selector[candidateGenerationLabel]
	if operationID == "" && !hasGeneration {
		return result, ErrCandidateStatusNotSelected
	}
	if !registryauth.ValidRevision(operationID) {
		return result, fmt.Errorf("candidate activation operation is invalid")
	}
	if service.DeletionTimestamp != nil {
		return result, fmt.Errorf("candidate Service is being deleted")
	}
	if len(service.Spec.Selector) != 2 || service.Spec.Selector[appOwnershipLabel] != appName || !candidateGenerationPattern.MatchString(generation) {
		return result, fmt.Errorf("candidate Service selector is unavailable")
	}
	expectedSelector := map[string]string{appOwnershipLabel: appName, candidateGenerationLabel: generation}
	if !maps.Equal(service.Spec.Selector, expectedSelector) {
		return result, fmt.Errorf("candidate Service selector drifted")
	}
	list, err := c.deployments.List(ctx, metav1.ListOptions{LabelSelector: labels.Set(expectedSelector).AsSelector().String()})
	if err != nil {
		return result, fmt.Errorf("list candidate Deployments for app %q: %w", appName, err)
	}
	if len(list.Items) != 1 {
		return result, fmt.Errorf("candidate Deployment is not uniquely selected")
	}
	deployment := &list.Items[0]
	if err := requireAppResourceOwnership("Deployment", deployment.Name, appName, deployment.Labels); err != nil {
		return result, err
	}
	if deployment.Name != candidateNameForGeneration(appName, generation) || deployment.Spec.Selector == nil || len(deployment.Spec.Selector.MatchExpressions) != 0 || !maps.Equal(deployment.Spec.Selector.MatchLabels, expectedSelector) || !labels.Set(expectedSelector).AsSelector().Matches(labels.Set(deployment.Spec.Template.Labels)) {
		return result, fmt.Errorf("candidate Deployment selector drifted")
	}
	result.DeploymentName = deployment.Name
	result.DesiredReplicas = deploymentReplicas(deployment)
	result.AvailableReplicas = deployment.Status.AvailableReplicas
	result.State = candidateStatusForDeployment(deployment)
	return result, nil
}

func deploymentReplicas(deployment *appsv1.Deployment) int32 {
	if deployment.Spec.Replicas == nil {
		return 0
	}
	return *deployment.Spec.Replicas
}

func candidateStatusForDeployment(deployment *appsv1.Deployment) string {
	desired := deploymentReplicas(deployment)
	if desired < 1 || deployment.Status.AvailableReplicas < 1 || deployment.DeletionTimestamp != nil {
		return StatusUnavailable
	}
	if deployment.Generation < 1 || deployment.Status.ObservedGeneration < deployment.Generation ||
		deployment.Status.Replicas != desired || deployment.Status.UpdatedReplicas != desired ||
		deployment.Status.ReadyReplicas != desired || deployment.Status.AvailableReplicas != desired ||
		deployment.Status.UnavailableReplicas != 0 {
		return StatusDegraded
	}
	return StatusHealthy
}
