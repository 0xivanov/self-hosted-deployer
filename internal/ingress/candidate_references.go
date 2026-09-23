package ingress

import (
	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"maps"
)

// CandidateRegistrySecretName derives the immutable reference without exposing
// registry credentials. Configuration validation is required before use.
func CandidateRegistrySecretName(cfg appconfig.Config) string {
	if cfg.ImagePullCredential == "" {
		return ""
	}
	return registrySecretName(cfg.Name, cfg.ImagePullCredential)
}

// IsInactiveCandidateTarget verifies bootstrap routing for a request that has
// no predecessor. It must not adopt an unexplained existing active Service.
func IsInactiveCandidateTarget(cfg appconfig.Config, requestID string, target ActivationTarget) bool {
	selector := map[string]string{appOwnershipLabel: cfg.Name, candidateGenerationLabel: "inactive-" + candidateGeneration(cfg.Name, requestID)}
	if !maps.Equal(target.Selector, selector) || len(target.Ports) != 1 {
		return false
	}
	port := target.Ports[0]
	return port.Name == "http" && port.Port == int32(cfg.Service.Port) && port.TargetPort == intstr.FromInt32(int32(cfg.Service.Port)) && (port.Protocol == "" || port.Protocol == corev1.ProtocolTCP)
}
