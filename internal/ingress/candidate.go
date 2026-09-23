package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const candidateGenerationLabel = "deployer.io/candidate-generation"

// CandidateDeploymentForApp renders an isolated, immutable candidate. It is
// deliberately limited to the hosted stateless profile. Candidate pods keep
// the deployer app ownership label, but omit app.kubernetes.io/name: the
// legacy Service cannot route to the candidate before activation. A separate
// candidate NetworkPolicy is required because the legacy policy does not select
// these pods. PrepareCandidateDeployment installs it before creating pods.
func CandidateDeploymentForApp(cfg appconfig.Config, namespace, secretRevision, requestID, registrySecretName string) (*appsv1.Deployment, error) {
	if strings.TrimSpace(requestID) == "" || !registryauth.ValidRevision(requestID) {
		return nil, errors.New("candidate request ID must be a 64 character lowercase hexadecimal value")
	}
	if cfg.Hosting == nil {
		return nil, errors.New("candidate deployments require the hosting profile")
	}
	if cfg.State.Mode != appconfig.DefaultStateMode {
		return nil, errors.New("candidate deployments require stateless state mode")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid candidate configuration: %w", err)
	}
	if err := ValidateCandidateReferences(cfg, secretRevision, registrySecretName); err != nil {
		return nil, err
	}
	desired, err := deploymentForApp(cfg, namespace, secretRevision)
	if err != nil {
		return nil, err
	}
	desired.Name = candidateDeploymentName(cfg.Name, requestID)
	generation := candidateGeneration(cfg.Name, requestID)
	if registrySecretName != "" {
		desired.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: registrySecretName}}
	}
	desired.Labels[candidateGenerationLabel] = generation
	selector := map[string]string{
		"deployer.io/app":        cfg.Name,
		candidateGenerationLabel: generation,
	}
	desired.Spec.Selector = &metav1.LabelSelector{MatchLabels: selector}
	desired.Spec.Template.Labels = map[string]string{
		"deployer.io/app":             cfg.Name,
		candidateGenerationLabel:      generation,
		"deployer.io/state-mode":      cfg.State.Mode,
		"deployer.io/resilience-mode": cfg.Resilience.Mode,
		hostingProfileLabel:           cfg.Hosting.Version,
	}
	for i := range desired.Spec.Template.Spec.TopologySpreadConstraints {
		desired.Spec.Template.Spec.TopologySpreadConstraints[i].LabelSelector = &metav1.LabelSelector{MatchLabels: selector}
	}
	if affinity := desired.Spec.Template.Spec.Affinity; affinity != nil && affinity.PodAntiAffinity != nil {
		for i := range affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
			affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution[i].PodAffinityTerm.LabelSelector = &metav1.LabelSelector{MatchLabels: selector}
		}
		for i := range affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
			affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[i].LabelSelector = &metav1.LabelSelector{MatchLabels: selector}
		}
	}
	return desired, nil
}

// CandidateSelector returns the selector used by a prepared candidate. It is
// exposed for a later activation gate; stable Services must never select a
// candidate using the legacy app selector.
func CandidateSelector(appName, requestID string) (map[string]string, error) {
	if strings.TrimSpace(appName) == "" || !registryauth.ValidRevision(requestID) {
		return nil, errors.New("invalid candidate identity")
	}
	return map[string]string{"deployer.io/app": appName, candidateGenerationLabel: candidateGeneration(appName, requestID)}, nil
}

func candidateGeneration(appName, requestID string) string {
	sum := sha256.Sum256([]byte(appName + "\x00" + requestID))
	return hex.EncodeToString(sum[:])[:32]
}

func candidateDeploymentName(appName, requestID string) string {
	return candidateNameForGeneration(appName, candidateGeneration(appName, requestID))
}

func candidateNameForGeneration(appName, generation string) string {
	suffix := "-candidate-" + generation
	name := strings.ToLower(strings.TrimSpace(appName))
	maxPrefix := 63 - len(suffix)
	if len(name) > maxPrefix {
		name = strings.TrimSuffix(name[:maxPrefix], "-")
	}
	return name + suffix
}

// PrepareCandidateDeployment creates the candidate exactly once. An existing
// object is accepted only when it is owned by the same app and is byte-for-
// byte equivalent after normalization of known Kubernetes defaults; it is
// never updated. Unexpected defaults or mutations fail closed.
func (c *Controller) PrepareCandidateDeployment(ctx context.Context, cfg appconfig.Config, secretRevision, requestID, registrySecretName string) error {
	if c.deployments == nil || c.networkPolicies == nil {
		return errors.New("candidate deployment runtime is unavailable")
	}
	desired, err := CandidateDeploymentForApp(cfg, c.namespace, secretRevision, requestID, registrySecretName)
	if err != nil {
		return err
	}
	policy, err := candidateNetworkPolicyForApp(cfg, c.namespace, requestID)
	if err != nil {
		return err
	}
	if err := c.ensureCandidateNetworkPolicy(ctx, policy); err != nil {
		return err
	}
	existing, err := c.deployments.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.deployments.Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create candidate deployment: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read candidate deployment: %w", err)
	}
	if err := requireAppResourceOwnership("Deployment", existing.Name, cfg.Name, existing.Labels); err != nil {
		return err
	}
	if !candidateDeploymentMatches(existing, desired) {
		return errors.New("candidate deployment already exists with different immutable content")
	}
	return nil
}

func candidateNetworkPolicyForApp(cfg appconfig.Config, namespace, requestID string) (*networkingv1.NetworkPolicy, error) {
	policy, err := networkPolicyForHostedApp(cfg, namespace)
	if err != nil {
		return nil, err
	}
	selector, err := CandidateSelector(cfg.Name, requestID)
	if err != nil {
		return nil, err
	}
	selector[hostingProfileLabel] = cfg.Hosting.Version
	policy.Name = candidateDeploymentName(cfg.Name, requestID)
	policy.Labels[candidateGenerationLabel] = candidateGeneration(cfg.Name, requestID)
	policy.Spec.PodSelector = metav1.LabelSelector{MatchLabels: selector}
	return policy, nil
}

func (c *Controller) ensureCandidateNetworkPolicy(ctx context.Context, desired *networkingv1.NetworkPolicy) error {
	existing, err := c.networkPolicies.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.networkPolicies.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create candidate NetworkPolicy: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read candidate NetworkPolicy: %w", err)
	}
	if err := requireAppResourceOwnership("NetworkPolicy", existing.Name, desired.Labels[appOwnershipLabel], existing.Labels); err != nil {
		return err
	}
	if !candidateNetworkPolicyMatches(existing, desired) {
		return errors.New("candidate NetworkPolicy already exists with different immutable content")
	}
	return nil
}

// ValidateCandidateReferences prevents a trusted coordinator from binding a
// candidate to secrets outside the immutable configuration saved in its journal.
func ValidateCandidateReferences(cfg appconfig.Config, revision, pullSecret string) error {
	if len(cfg.Secrets) > 0 || revision != cfg.EnvironmentRevision {
		return errors.New("candidate environment reference does not match saved configuration")
	}
	expected := ""
	if cfg.ImagePullCredential != "" {
		expected = registrySecretName(cfg.Name, cfg.ImagePullCredential)
	}
	if pullSecret != expected {
		return errors.New("candidate registry reference does not match saved configuration")
	}
	return nil
}
