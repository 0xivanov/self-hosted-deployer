package ingress

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ErrCandidateNotReady = errors.New("candidate has not reached full readiness")

// ActivatePreparedCandidate connects the immutable preparation and conditional
// traffic switch. It only reads candidate resources; it cannot repair a changed
// candidate, refresh a stale gate, or replay a failed activation automatically.
// The coordinator must persist the gate and own legacy-writer exclusion.
func (c *Controller) ActivatePreparedCandidate(ctx context.Context, gate ActivationGate, cfg appconfig.Config, secretRevision, requestID, registrySecretName string) (ActivationGate, error) {
	if gate.App != cfg.Name || gate.OperationID != requestID {
		return ActivationGate{}, ErrActivationSuperseded
	}
	if c.deployments == nil || c.networkPolicies == nil || c.services == nil {
		return ActivationGate{}, errors.New("candidate activation runtime is unavailable")
	}
	desired, err := CandidateDeploymentForApp(cfg, c.namespace, secretRevision, requestID, registrySecretName)
	if err != nil {
		return ActivationGate{}, err
	}
	actual, err := c.deployments.Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		return ActivationGate{}, err
	}
	if !candidateDeploymentMatches(actual, desired) {
		return ActivationGate{}, errors.New("candidate configuration changed")
	}
	if actual.DeletionTimestamp != nil || actual.UID == "" || actual.Generation < 1 || actual.Status.ObservedGeneration < actual.Generation || desired.Spec.Replicas == nil {
		return ActivationGate{}, ErrCandidateNotReady
	}
	replicas := *desired.Spec.Replicas
	if replicas < 1 || actual.Status.Replicas != replicas || actual.Status.UpdatedReplicas != replicas || actual.Status.ReadyReplicas != replicas || actual.Status.AvailableReplicas != replicas || actual.Status.UnavailableReplicas != 0 {
		return ActivationGate{}, ErrCandidateNotReady
	}
	policy, err := candidateNetworkPolicyForApp(cfg, c.namespace, requestID)
	if err != nil {
		return ActivationGate{}, err
	}
	installed, err := c.networkPolicies.Get(ctx, policy.Name, metav1.GetOptions{})
	if err != nil {
		return ActivationGate{}, err
	}
	if err = requireAppResourceOwnership("NetworkPolicy", installed.Name, cfg.Name, installed.Labels); err != nil {
		return ActivationGate{}, err
	}
	if installed.DeletionTimestamp != nil || !candidateNetworkPolicyMatches(installed, policy) {
		return ActivationGate{}, errors.New("candidate network isolation changed")
	}
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return ActivationGate{}, err
	}
	// Port changes also require ingress coordination, which is deliberately not
	// inferred from the candidate. Do not switch traffic with a mismatched route.
	target := serviceForApp(cfg, c.namespace)
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Port != target.Spec.Ports[0].Port {
		return ActivationGate{}, fmt.Errorf("candidate port change requires route coordination")
	}
	return c.ReplaceActivationTarget(ctx, gate, ActivationTarget{Selector: desired.Spec.Selector.MatchLabels, Ports: target.Spec.Ports})
}
