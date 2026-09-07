package ingress

import (
	"context"
	"fmt"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const hostingProfileLabel = "deployer.io/hosting-profile"

func networkPolicyForHostedApp(cfg appconfig.Config, namespace string) (*networkingv1.NetworkPolicy, error) {
	if cfg.Hosting == nil {
		return nil, nil
	}
	if strings.TrimSpace(namespace) == "" {
		namespace = DefaultNamespace
	}
	labels := managedAppLabels(cfg.Name)
	labels[hostingProfileLabel] = cfg.Hosting.Version
	selectorLabels := appLabels(cfg.Name)
	selectorLabels[hostingProfileLabel] = cfg.Hosting.Version
	servicePort := networkingv1.NetworkPolicyPort{Protocol: protocolPtr(corev1.ProtocolTCP), Port: intPort(int32(cfg.Service.Port))}
	ingress := []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}},
				PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "traefik"}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{servicePort},
		},
	}
	monitoringPorts := []networkingv1.NetworkPolicyPort{servicePort}
	if cfg.Metrics != nil {
		monitoringPorts = append(monitoringPorts, networkingv1.NetworkPolicyPort{Protocol: protocolPtr(corev1.ProtocolTCP), Port: intPort(int32(cfg.Metrics.Port))})
	}
	ingress = append(ingress, networkingv1.NetworkPolicyIngressRule{
		From:  []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "deployer-monitoring"}}}},
		Ports: monitoringPorts,
	})

	egress := []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}},
				PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: protocolPtr(corev1.ProtocolUDP), Port: intPort(53)},
				{Protocol: protocolPtr(corev1.ProtocolTCP), Port: intPort(53)},
			},
		},
	}
	for _, rule := range cfg.Hosting.Network.Egress {
		ports := make([]networkingv1.NetworkPolicyPort, 0, len(rule.Ports))
		for _, port := range rule.Ports {
			ports = append(ports, networkingv1.NetworkPolicyPort{Protocol: protocolPtr(corev1.ProtocolTCP), Port: intPort(int32(port))})
		}
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{
			To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: rule.CIDR}}},
			Ports: ports,
		})
	}
	return &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: namespace, Labels: labels},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: selectorLabels},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress:     ingress,
			Egress:      egress,
		},
	}, nil
}

func (c *Controller) reconcileHostingNetworkPolicy(ctx context.Context, cfg appconfig.Config) error {
	if cfg.Hosting == nil {
		return nil
	}
	if c.networkPolicies == nil {
		return fmt.Errorf("hosting profile v1 is unsupported by this runtime: NetworkPolicy client is required")
	}
	desired, err := networkPolicyForHostedApp(cfg, c.namespace)
	if err != nil {
		return err
	}
	existing, err := c.networkPolicies.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.networkPolicies.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create NetworkPolicy %q: %w", desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get NetworkPolicy %q: %w", desired.Name, err)
	}
	if err := requireHostingPolicyOwnership(existing, cfg.Name); err != nil {
		return err
	}
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := c.networkPolicies.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update NetworkPolicy %q: %w", desired.Name, err)
	}
	return nil
}

func requireHostingPolicyOwnership(policy *networkingv1.NetworkPolicy, appName string) error {
	if err := requireAppResourceOwnership("NetworkPolicy", policy.Name, appName, policy.Labels); err != nil {
		return err
	}
	if policy.Labels[hostingProfileLabel] != "v1" {
		return fmt.Errorf("NetworkPolicy %q ownership conflict: expected label %s=%q, found %q", policy.Name, hostingProfileLabel, "v1", policy.Labels[hostingProfileLabel])
	}
	return nil
}

func (c *Controller) deleteHostingNetworkPolicy(ctx context.Context, appName string) error {
	if c.networkPolicies == nil {
		return nil
	}
	existing, err := c.networkPolicies.Get(ctx, appName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get NetworkPolicy %q for deletion: %w", appName, err)
	}
	if existing.Labels[hostingProfileLabel] != "v1" {
		return nil
	}
	if err := requireHostingPolicyOwnership(existing, appName); err != nil {
		return err
	}
	// Deployment deletion is asynchronous. Keep isolation until every matching
	// Pod has disappeared, including terminating Pods. A later cleanup may
	// remove the policy once the workload is gone.
	if c.pods == nil {
		return nil
	}
	pods, err := c.pods.List(ctx, metav1.ListOptions{LabelSelector: "deployer.io/app=" + appName + "," + hostingProfileLabel + "=v1"})
	if err != nil {
		return fmt.Errorf("check Pods before deleting hosting policy: %w", err)
	}
	if len(pods.Items) != 0 {
		return nil
	}
	if err := c.networkPolicies.Delete(ctx, appName, ownedDeleteOptions(existing)); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete NetworkPolicy %q: %w", appName, err)
	}
	return nil
}

func protocolPtr(value corev1.Protocol) *corev1.Protocol { return &value }

func intPort(value int32) *intstr.IntOrString {
	port := intstr.FromInt32(value)
	return &port
}
