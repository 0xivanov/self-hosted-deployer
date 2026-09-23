package ingress

import (
	"context"
	"errors"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const initialCandidateRequestAnnotation = "deployer.io/initial-candidate-request"

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
	service := serviceForApp(cfg, c.namespace)
	// Candidate generation values are 32 hex characters, so none can match
	// this explicit inactive selector. An empty selector would be unsafe.
	service.Spec.Selector = map[string]string{appOwnershipLabel: cfg.Name, candidateGenerationLabel: "inactive-" + candidateGeneration(cfg.Name, requestID)}
	service.Annotations[initialCandidateRequestAnnotation] = requestID
	_, err := c.services.Create(ctx, service, metav1.CreateOptions{})
	return err
}
