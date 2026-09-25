package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const initialCandidateRequestAnnotation = "deployer.io/initial-candidate-request"

// CandidateBootstrapOperationID is the deterministic temporary fence used
// while a withdrawn bootstrap is rebound to a new request.
func CandidateBootstrapOperationID(app, requestID string) string {
	sum := sha256.Sum256([]byte("candidate-bootstrap-v1\x00" + app + "\x00" + requestID))
	return hex.EncodeToString(sum[:])
}

// InitialCandidateRequest returns the durable request identity attached to an
// inactive bootstrap Service. Callers must still validate the request and its
// recovery proof before using it.
func (c *Controller) InitialCandidateRequest(ctx context.Context, gate ActivationGate) (string, error) {
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return "", err
	}
	requestID := service.Annotations[initialCandidateRequestAnnotation]
	if !registryauth.ValidRevision(requestID) {
		return "", errors.New("inactive candidate service has no valid request identity")
	}
	return requestID, nil
}

// ResetInactiveCandidateService rebinds a previously withdrawn initial
// bootstrap to a new request. It preserves the Service UID and uses the
// resourceVersion gate, so delayed writers for the old request cannot win.
// The server proves the old request's withdrawal, recovery gate, binding and
// retired candidate before calling this method.
func (c *Controller) ResetInactiveCandidateService(ctx context.Context, gate ActivationGate, cfg appconfig.Config, oldRequestID, newRequestID string) (ActivationGate, ActivationTarget, error) {
	if !registryauth.ValidRevision(oldRequestID) || !registryauth.ValidRevision(newRequestID) || oldRequestID == newRequestID {
		return ActivationGate{}, ActivationTarget{}, errors.New("invalid candidate bootstrap reuse identities")
	}
	if gate.App != cfg.Name || cfg.Validate() != nil || cfg.Hosting == nil {
		return ActivationGate{}, ActivationTarget{}, errors.New("invalid candidate bootstrap configuration")
	}
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return ActivationGate{}, ActivationTarget{}, err
	}
	if service.DeletionTimestamp != nil {
		return ActivationGate{}, ActivationTarget{}, errors.New("candidate bootstrap service is being deleted")
	}
	if service.Annotations[initialCandidateRequestAnnotation] != oldRequestID {
		return ActivationGate{}, ActivationTarget{}, errors.New("inactive candidate bootstrap request changed")
	}
	selector, err := CandidateSelector(cfg.Name, newRequestID)
	if err != nil {
		return ActivationGate{}, ActivationTarget{}, err
	}
	selector[candidateGenerationLabel] = "inactive-" + candidateGeneration(cfg.Name, newRequestID)
	service.Spec.Selector = maps.Clone(selector)
	service.Spec.Ports = candidateServiceForApp(cfg, c.namespace).Spec.Ports
	service.Annotations[initialCandidateRequestAnnotation] = newRequestID
	service.Annotations[activationOperationAnnotation] = CandidateBootstrapOperationID(cfg.Name, newRequestID)
	updated, err := c.updateActivationGate(ctx, service)
	if err != nil {
		return ActivationGate{}, ActivationTarget{}, err
	}
	return updated, ActivationTarget{Selector: maps.Clone(service.Spec.Selector), Ports: append(service.Spec.Ports[:0:0], service.Spec.Ports...)}, nil
}

// CreateInactiveCandidateService is the create-only bootstrap for a new hosted
// app. Persist the request before calling. The Service selects no candidate and
// does not expose an Ingress. Existing services are never adopted or overwritten;
// an ambiguous create must be inspected by the durable coordinator.
func (c *Controller) CreateInactiveCandidateService(ctx context.Context, cfg appconfig.Config, requestID string) error {
	pullSecret := ""
	if cfg.ImagePullCredential != "" {
		pullSecret = registrySecretName(cfg.Name, cfg.ImagePullCredential)
	}
	if _, err := CandidateDeploymentForApp(cfg, c.namespace, cfg.EnvironmentRevision, requestID, pullSecret); err != nil {
		return err
	}
	if c.services == nil {
		return errors.New("candidate service runtime is unavailable")
	}
	service := candidateServiceForApp(cfg, c.namespace)
	// Candidate generation values are 32 hex characters, so none can match
	// this explicit inactive selector. An empty selector would be unsafe.
	service.Spec.Selector = map[string]string{appOwnershipLabel: cfg.Name, candidateGenerationLabel: "inactive-" + candidateGeneration(cfg.Name, requestID)}
	service.Annotations[initialCandidateRequestAnnotation] = requestID
	_, err := c.services.Create(ctx, service, metav1.CreateOptions{})
	return err
}

// Candidate gates compare ports exactly. Set the API default explicitly so
// saved targets match the Service returned by Kubernetes.
func candidateServiceForApp(cfg appconfig.Config, namespace string) *corev1.Service {
	service := serviceForApp(cfg, namespace)
	for i := range service.Spec.Ports {
		service.Spec.Ports[i].Protocol = corev1.ProtocolTCP
	}
	return service
}
