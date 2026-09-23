package ingress

import (
	"context"
	"errors"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
)

// PrepareCandidateDependencies admits the overlapping rollout before creating
// immutable secrets. It never reconciles the stable Deployment, Service, route,
// or legacy mutable app secret. Call before PrepareCandidateDeployment.
func (c *Controller) PrepareCandidateDependencies(ctx context.Context, cfg appconfig.Config, values map[string]string, revision, requestID string, credential *registryauth.Credential) (string, error) {
	if len(cfg.Secrets) > 0 || (cfg.EnvironmentRevision == "" && (revision != "" || len(values) > 0)) {
		return "", errors.New("candidate deployments require versioned environment settings")
	}
	if err := validateEnvironmentReference(cfg, values, revision); err != nil {
		return "", err
	}
	if err := validateRegistryReference(cfg, credential); err != nil {
		return "", err
	}
	name := ""
	if cfg.ImagePullCredential != "" {
		name = registrySecretName(cfg.Name, cfg.ImagePullCredential)
	}
	if _, err := CandidateDeploymentForApp(cfg, c.namespace, revision, requestID, name); err != nil {
		return "", err
	}
	if c.namespaces == nil || ((cfg.EnvironmentRevision != "" || credential != nil) && c.appSecrets == nil) {
		return "", errors.New("candidate dependency runtime is unavailable")
	}
	// Existing predecessor pods remain included in capacity usage. The extra
	// candidate reservation must fit alongside them, including rollout surge.
	if err := c.PreflightHosting(ctx, cfg); err != nil {
		return "", err
	}
	if err := c.ensureSchedulableWorker(ctx, cfg); err != nil {
		return "", err
	}
	if err := c.reconcileNamespace(ctx); err != nil {
		return "", err
	}
	if cfg.EnvironmentRevision != "" {
		if err := c.reconcileEnvironmentSecret(ctx, cfg, values); err != nil {
			return "", err
		}
	}
	return c.reconcileRegistrySecret(ctx, cfg, credential)
}
