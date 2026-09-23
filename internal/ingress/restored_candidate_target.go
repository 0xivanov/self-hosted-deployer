package ingress

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RestoredCandidateTargetReady verifies the exact target selected by a saved
// activation gate before a withdrawal is committed. It is read-only and
// fails closed on ownership, selector, workload, or readiness drift.
func (c *Controller) RestoredCandidateTargetReady(ctx context.Context, gate ActivationGate, target ActivationTarget, previous appconfig.Config) (bool, error) {
	if c.services == nil || c.deployments == nil {
		return false, errors.New("restored target runtime is unavailable")
	}
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return false, err
	}
	if previous.Name != gate.App || service.DeletionTimestamp != nil {
		return false, errors.New("restored activation service identity changed")
	}
	if err := previous.Validate(); err != nil {
		return false, fmt.Errorf("invalid predecessor configuration: %w", err)
	}
	if !maps.Equal(service.Spec.Selector, target.Selector) || !apiequality.Semantic.DeepEqual(service.Spec.Ports, target.Ports) {
		return false, errors.New("restored activation target changed")
	}
	expectedService := serviceForApp(previous, c.namespace)
	if !apiequality.Semantic.DeepEqual(target.Ports, expectedService.Spec.Ports) {
		return false, errors.New("restored activation ports changed")
	}
	if len(target.Selector) == 0 {
		return false, errors.New("restored activation target is empty")
	}

	var expectedName string
	var expected = (*appsv1.Deployment)(nil)
	if maps.Equal(target.Selector, appLabels(previous.Name)) {
		if len(previous.Secrets) > 0 && previous.EnvironmentRevision == "" {
			return false, errors.New("mutable legacy secrets cannot be restored")
		}
		registryName := ""
		if previous.ImagePullCredential != "" {
			registryName = registrySecretName(previous.Name, previous.ImagePullCredential)
		}
		expected, err = deploymentForApp(previous, c.namespace, previous.EnvironmentRevision)
		if err == nil && registryName != "" {
			expected.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: registryName}}
		}
		expectedName = previous.Name
	} else {
		if previous.Hosting == nil || previous.State.Mode != appconfig.DefaultStateMode {
			return false, errors.New("restored candidate profile is unsupported")
		}
		generation, ok := target.Selector[candidateGenerationLabel]
		if !ok || len(target.Selector) != 2 || target.Selector[appOwnershipLabel] != previous.Name || !candidateGenerationPattern.MatchString(generation) {
			return false, errors.New("restored activation target selector is invalid")
		}
		registryName := ""
		if previous.ImagePullCredential != "" {
			registryName = registrySecretName(previous.Name, previous.ImagePullCredential)
		}
		expected, err = candidateDeploymentForGeneration(previous, c.namespace, previous.EnvironmentRevision, generation, registryName)
		expectedName = candidateNameForGeneration(previous.Name, generation)
	}
	if err != nil {
		return false, fmt.Errorf("render restored target: %w", err)
	}
	if expected.Name != expectedName {
		return false, errors.New("restored target deployment identity changed")
	}
	actual, err := c.deployments.Get(ctx, expectedName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get restored target Deployment: %w", err)
	}
	if actual.UID == "" {
		return false, errors.New("restored target Deployment lacks identity")
	}
	if err := requireAppResourceOwnership("Deployment", actual.Name, previous.Name, actual.Labels); err != nil {
		return false, err
	}
	if !candidateDeploymentMatches(actual, expected) {
		return false, errors.New("restored target deployment changed")
	}
	if actual.DeletionTimestamp != nil || actual.Generation < 1 || actual.Status.ObservedGeneration < actual.Generation || actual.Spec.Replicas == nil {
		return false, nil
	}
	replicas := *actual.Spec.Replicas
	if replicas < 1 || actual.Status.Replicas != replicas || actual.Status.UpdatedReplicas != replicas || actual.Status.ReadyReplicas != replicas || actual.Status.AvailableReplicas != replicas || actual.Status.UnavailableReplicas != 0 {
		return false, nil
	}
	return true, nil
}
