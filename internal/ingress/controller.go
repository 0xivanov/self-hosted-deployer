package ingress

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	appstyped "k8s.io/client-go/kubernetes/typed/apps/v1"
	coretyped "k8s.io/client-go/kubernetes/typed/core/v1"
	networkingtyped "k8s.io/client-go/kubernetes/typed/networking/v1"
	policytyped "k8s.io/client-go/kubernetes/typed/policy/v1"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	StatusHealthy     = "healthy"
	StatusDegraded    = "degraded"
	StatusUnavailable = "unavailable"
)

var clusterIssuerResource = schema.GroupVersionResource{
	Group:    "cert-manager.io",
	Version:  "v1",
	Resource: "clusterissuers",
}

type ControllerConfig struct {
	KubeconfigPath string
	Namespace      string
	TLS            TLSConfig
}

type Controller struct {
	namespace   string
	tls         TLSConfig
	namespaces  coretyped.NamespaceInterface
	ingresses   networkingtyped.IngressInterface
	services    coretyped.ServiceInterface
	appSecrets  coretyped.SecretInterface
	nodes       coretyped.NodeInterface
	pods        coretyped.PodInterface
	evictions   policytyped.EvictionInterface
	pdbs        policytyped.PodDisruptionBudgetInterface
	deployments appstyped.DeploymentInterface
	issuers     dynamic.ResourceInterface
}

func NewController(cfg ControllerConfig) (*Controller, error) {
	namespace := strings.TrimSpace(cfg.Namespace)
	if namespace == "" {
		namespace = DefaultNamespace
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", cfg.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes config for ingress: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes ingress client: %w", err)
	}

	tlsConfig := cfg.TLS.WithDefaults()
	var issuers dynamic.ResourceInterface
	if tlsConfig.Enabled() {
		dynamicClient, err := dynamic.NewForConfig(restConfig)
		if err != nil {
			return nil, fmt.Errorf("create cert-manager client: %w", err)
		}
		issuers = dynamicClient.Resource(clusterIssuerResource)
	}

	return &Controller{
		namespace:   namespace,
		tls:         tlsConfig,
		namespaces:  clientset.CoreV1().Namespaces(),
		ingresses:   clientset.NetworkingV1().Ingresses(namespace),
		services:    clientset.CoreV1().Services(namespace),
		appSecrets:  clientset.CoreV1().Secrets(namespace),
		nodes:       clientset.CoreV1().Nodes(),
		pods:        clientset.CoreV1().Pods(namespace),
		evictions:   clientset.PolicyV1().Evictions(namespace),
		pdbs:        clientset.PolicyV1().PodDisruptionBudgets(namespace),
		deployments: clientset.AppsV1().Deployments(namespace),
		issuers:     issuers,
	}, nil
}

func (c *Controller) TLSEnabled() bool {
	return c.tls.Enabled()
}

func (c *Controller) Reconcile(ctx context.Context, cfg appconfig.Config, secretValues map[string]string, secretRevision string) error {
	if err := c.ensureSchedulableWorker(ctx, cfg); err != nil {
		return err
	}
	if err := c.reconcileAppResources(ctx, cfg, secretValues, secretRevision); err != nil {
		return err
	}
	manifest, ok, err := ManifestForApp(cfg, c.namespace, c.tls)
	if err != nil {
		return err
	}
	if !ok {
		return c.deleteIngress(ctx, cfg.Name)
	}
	if c.tls.Enabled() {
		if err := c.ensureIssuer(ctx); err != nil {
			return err
		}
	}

	desired := kubernetesIngress(manifest)
	existing, err := c.ingresses.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.ingresses.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create Ingress %q: %w", desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get Ingress %q: %w", desired.Name, err)
	}

	desired.ResourceVersion = existing.ResourceVersion
	if _, err := c.ingresses.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Ingress %q: %w", desired.Name, err)
	}
	return nil
}

func (c *Controller) Delete(ctx context.Context, appName string) error {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return nil
	}
	return errors.Join(
		c.deleteIngress(ctx, appName),
		c.deleteService(ctx, appName),
		c.deleteDeployment(ctx, appName),
		c.deletePodDisruptionBudget(ctx, appName),
		c.deleteAppSecret(ctx, appName),
	)
}

func (c *Controller) deleteIngress(ctx context.Context, appName string) error {
	err := c.ingresses.Delete(ctx, appName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete Ingress %q: %w", appName, err)
	}
	return nil
}

func (c *Controller) Status(ctx context.Context, appName string) (string, error) {
	deployment, err := c.deployments.Get(ctx, appName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return StatusUnavailable, nil
	}
	if err != nil {
		return "", fmt.Errorf("get Deployment %q for route status: %w", appName, err)
	}
	return statusForDeployment(deployment), nil
}

func (c *Controller) StatusDetails(ctx context.Context, appName string) (string, int32, int32, error) {
	deployment, err := c.deployments.Get(ctx, appName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return StatusUnavailable, 0, 0, nil
	}
	if err != nil {
		return "", 0, 0, fmt.Errorf("get Deployment %q for app status: %w", appName, err)
	}
	var desired int32
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	return statusForDeployment(deployment), desired, deployment.Status.AvailableReplicas, nil
}

func (c *Controller) ensureIssuer(ctx context.Context) error {
	if c.issuers == nil {
		return fmt.Errorf("cert-manager client is required for TLS ingress")
	}
	desired := clusterIssuer(c.tls)
	existing, err := c.issuers.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.issuers.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create ClusterIssuer %q: %w", desired.GetName(), err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get ClusterIssuer %q: %w", desired.GetName(), err)
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	if _, err := c.issuers.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update ClusterIssuer %q: %w", desired.GetName(), err)
	}
	return nil
}

func statusForDeployment(deployment *appsv1.Deployment) string {
	var desired int32
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	available := deployment.Status.AvailableReplicas
	switch {
	case desired < 1 || available < 1:
		return StatusUnavailable
	case available < desired:
		return StatusDegraded
	default:
		return StatusHealthy
	}
}

func kubernetesIngress(manifest Manifest) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	rules := make([]networkingv1.IngressRule, 0, len(manifest.Spec.Rules))
	for _, rule := range manifest.Spec.Rules {
		paths := make([]networkingv1.HTTPIngressPath, 0, len(rule.HTTP.Paths))
		for _, path := range rule.HTTP.Paths {
			paths = append(paths, networkingv1.HTTPIngressPath{
				Path:     path.Path,
				PathType: &pathType,
				Backend: networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{
						Name: path.Backend.Service.Name,
						Port: networkingv1.ServiceBackendPort{
							Number: int32(path.Backend.Service.Port.Number),
						},
					},
				},
			})
		}
		rules = append(rules, networkingv1.IngressRule{
			Host: rule.Host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{Paths: paths},
			},
		})
	}
	tlsEntries := make([]networkingv1.IngressTLS, 0, len(manifest.Spec.TLS))
	for _, tlsEntry := range manifest.Spec.TLS {
		tlsEntries = append(tlsEntries, networkingv1.IngressTLS{
			Hosts:      tlsEntry.Hosts,
			SecretName: tlsEntry.SecretName,
		})
	}
	className := manifest.Spec.IngressClassName
	return &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: manifest.APIVersion,
			Kind:       manifest.Kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        manifest.Metadata.Name,
			Namespace:   manifest.Metadata.Namespace,
			Labels:      manifest.Metadata.Labels,
			Annotations: manifest.Metadata.Annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS:              tlsEntries,
			Rules:            rules,
		},
	}
}

func clusterIssuer(tlsConfig TLSConfig) *unstructured.Unstructured {
	tlsConfig = tlsConfig.WithDefaults()
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "ClusterIssuer",
		"metadata": map[string]any{
			"name": tlsConfig.ClusterIssuer,
		},
		"spec": map[string]any{
			"acme": map[string]any{
				"email":  tlsConfig.ACMEEmail,
				"server": tlsConfig.ACMEServer,
				"privateKeySecretRef": map[string]any{
					"name": tlsConfig.ClusterIssuer + "-account-key",
				},
				"solvers": []any{map[string]any{
					"http01": map[string]any{
						"ingress": map[string]any{"ingressClassName": "traefik"},
					},
				}},
			},
		},
	}}
}
