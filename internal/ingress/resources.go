package ingress

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const secretHashAnnotation = "deployer.io/secret-hash"

func (c *Controller) reconcileAppResources(ctx context.Context, cfg appconfig.Config, secretValues map[string]string, secretRevision string) error {
	if err := c.reconcileNamespace(ctx); err != nil {
		return err
	}
	if err := c.reconcileSecret(ctx, cfg, secretValues); err != nil {
		return err
	}
	if err := c.reconcileDeployment(ctx, cfg, secretRevision); err != nil {
		return err
	}
	if err := c.reconcilePodDisruptionBudget(ctx, cfg); err != nil {
		return err
	}
	if err := c.reconcileService(ctx, cfg); err != nil {
		return err
	}
	return nil
}

func (c *Controller) reconcilePodDisruptionBudget(ctx context.Context, cfg appconfig.Config) error {
	if cfg.Resilience.Mode != appconfig.ResilienceResilient {
		return c.deletePodDisruptionBudget(ctx, cfg.Name)
	}
	desired := podDisruptionBudgetForApp(cfg, c.namespace)
	existing, err := c.pdbs.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.pdbs.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create PodDisruptionBudget %q: %w", desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get PodDisruptionBudget %q: %w", desired.Name, err)
	}
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := c.pdbs.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update PodDisruptionBudget %q: %w", desired.Name, err)
	}
	return nil
}

func (c *Controller) reconcileNamespace(ctx context.Context) error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "deployer",
			},
		},
	}
	_, err := c.namespaces.Get(ctx, c.namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.namespaces.Create(ctx, namespace, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create Namespace %q: %w", c.namespace, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get Namespace %q: %w", c.namespace, err)
	}
	return nil
}

func (c *Controller) reconcileDeployment(ctx context.Context, cfg appconfig.Config, secretRevision string) error {
	desired, err := deploymentForApp(cfg, c.namespace, secretRevision)
	if err != nil {
		return err
	}
	existing, err := c.deployments.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.deployments.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create Deployment %q: %w", desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get Deployment %q: %w", desired.Name, err)
	}
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := c.deployments.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Deployment %q: %w", desired.Name, err)
	}
	return nil
}

func (c *Controller) reconcileSecret(ctx context.Context, cfg appconfig.Config, secretValues map[string]string) error {
	if len(cfg.Secrets) == 0 {
		return c.deleteAppSecret(ctx, cfg.Name)
	}
	desired, err := secretForApp(cfg, c.namespace, secretValues)
	if err != nil {
		return err
	}
	existing, err := c.appSecrets.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.appSecrets.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create Secret %q: %w", desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get Secret %q: %w", desired.Name, err)
	}
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := c.appSecrets.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Secret %q: %w", desired.Name, err)
	}
	return nil
}

func (c *Controller) reconcileService(ctx context.Context, cfg appconfig.Config) error {
	desired := serviceForApp(cfg, c.namespace)
	existing, err := c.services.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.services.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create Service %q: %w", desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get Service %q: %w", desired.Name, err)
	}
	desired.ResourceVersion = existing.ResourceVersion
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	desired.Spec.ClusterIPs = existing.Spec.ClusterIPs
	desired.Spec.IPFamilies = existing.Spec.IPFamilies
	desired.Spec.IPFamilyPolicy = existing.Spec.IPFamilyPolicy
	if _, err := c.services.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Service %q: %w", desired.Name, err)
	}
	return nil
}

func (c *Controller) deleteDeployment(ctx context.Context, appName string) error {
	err := c.deployments.Delete(ctx, appName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete Deployment %q: %w", appName, err)
	}
	return nil
}

func (c *Controller) deletePodDisruptionBudget(ctx context.Context, appName string) error {
	if c.pdbs == nil {
		return nil
	}
	err := c.pdbs.Delete(ctx, appName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete PodDisruptionBudget %q: %w", appName, err)
	}
	return nil
}

func (c *Controller) deleteService(ctx context.Context, appName string) error {
	err := c.services.Delete(ctx, appName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete Service %q: %w", appName, err)
	}
	return nil
}

func (c *Controller) deleteAppSecret(ctx context.Context, appName string) error {
	err := c.appSecrets.Delete(ctx, appName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete Secret %q: %w", appName, err)
	}
	return nil
}

func deploymentForApp(cfg appconfig.Config, namespace string, secretRevision string) (*appsv1.Deployment, error) {
	cfg.Normalize()
	labels := appLabels(cfg.Name)
	podLabels := appLabels(cfg.Name)
	podLabels["deployer.io/state-mode"] = cfg.State.Mode
	podLabels["deployer.io/resilience-mode"] = cfg.Resilience.Mode
	replicas := int32(cfg.Deploy.Replicas)
	if cfg.Resilience.Mode == appconfig.ResilienceResilient && replicas < 2 {
		replicas = 2
	}
	port := int32(cfg.Service.Port)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  cfg.Name,
						Image: cfg.Image,
						Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: port}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: cfg.Service.Health.Path,
									Port: intstr.FromInt32(port),
								},
							},
						},
					}},
				},
			},
		},
	}
	if arch := placementArchitecture(cfg.Placement.Arch); arch != "" {
		deployment.Spec.Template.Spec.NodeSelector = map[string]string{"kubernetes.io/arch": arch}
	}
	if cfg.Resilience.Mode == appconfig.ResiliencePinned {
		if len(cfg.Placement.Prefer) != 1 {
			return nil, fmt.Errorf("pinned resilience requires exactly one preferred node selector")
		}
		if deployment.Spec.Template.Spec.NodeSelector == nil {
			deployment.Spec.Template.Spec.NodeSelector = map[string]string{}
		}
		for key, value := range cfg.Placement.Prefer[0] {
			deployment.Spec.Template.Spec.NodeSelector[placementLabelKey(key)] = value
		}
	}
	if cfg.Placement.Spread || cfg.Resilience.Mode == appconfig.ResilienceResilient {
		whenUnsatisfiable := corev1.ScheduleAnyway
		if cfg.Resilience.Mode == appconfig.ResilienceResilient {
			whenUnsatisfiable = corev1.DoNotSchedule
		}
		deployment.Spec.Template.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
			MaxSkew:           1,
			TopologyKey:       "kubernetes.io/hostname",
			WhenUnsatisfiable: whenUnsatisfiable,
			LabelSelector:     &metav1.LabelSelector{MatchLabels: labels},
		}}
	}
	if cfg.Resilience.Mode == appconfig.ResilienceResilient {
		maxUnavailable := intstr.FromInt32(1)
		maxSurge := intstr.FromInt32(0)
		deployment.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxUnavailable: &maxUnavailable,
				MaxSurge:       &maxSurge,
			},
		}
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
					LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
					TopologyKey:   "kubernetes.io/hostname",
				}},
			},
		}
	}
	if cfg.Resilience.Mode == appconfig.ResilienceFallback {
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: append(
					preferredSchedulingTerms(cfg.Placement.Prefer, 100),
					preferredSchedulingTerms(cfg.Placement.Fallback, 50)...,
				),
			},
		}
	}
	if len(cfg.Secrets) > 0 {
		if strings.TrimSpace(secretRevision) == "" {
			return nil, fmt.Errorf("secret revision is missing")
		}
		deployment.Spec.Template.Annotations = map[string]string{secretHashAnnotation: secretRevision}
		for _, name := range cfg.Secrets {
			deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name: name,
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: cfg.Name},
					Key:                  name,
				}},
			})
		}
	}
	return deployment, nil
}

func podDisruptionBudgetForApp(cfg appconfig.Config, namespace string) *policyv1.PodDisruptionBudget {
	minAvailable := intstr.FromInt32(1)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: namespace,
			Labels:    appLabels(cfg.Name),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector:     &metav1.LabelSelector{MatchLabels: appLabels(cfg.Name)},
		},
	}
}

func preferredSchedulingTerms(selectors []map[string]string, weight int32) []corev1.PreferredSchedulingTerm {
	terms := make([]corev1.PreferredSchedulingTerm, 0, len(selectors))
	for _, selector := range selectors {
		keys := make([]string, 0, len(selector))
		for key := range selector {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		requirements := make([]corev1.NodeSelectorRequirement, 0, len(keys))
		for _, key := range keys {
			requirements = append(requirements, corev1.NodeSelectorRequirement{
				Key:      placementLabelKey(key),
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{selector[key]},
			})
		}
		if len(requirements) > 0 {
			terms = append(terms, corev1.PreferredSchedulingTerm{
				Weight:     weight,
				Preference: corev1.NodeSelectorTerm{MatchExpressions: requirements},
			})
		}
	}
	return terms
}

func secretForApp(cfg appconfig.Config, namespace string, secretValues map[string]string) (*corev1.Secret, error) {
	cfg.Normalize()
	data := make(map[string][]byte, len(cfg.Secrets))
	for _, name := range cfg.Secrets {
		value, ok := secretValues[name]
		if !ok {
			return nil, fmt.Errorf("required secret %q is missing", name)
		}
		data[name] = []byte(value)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: namespace,
			Labels:    appLabels(cfg.Name),
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}, nil
}

func serviceForApp(cfg appconfig.Config, namespace string) *corev1.Service {
	cfg.Normalize()
	port := int32(cfg.Service.Port)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: namespace,
			Labels:    appLabels(cfg.Name),
		},
		Spec: corev1.ServiceSpec{
			Selector: appLabels(cfg.Name),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       port,
				TargetPort: intstr.FromInt32(port),
			}},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

func appLabels(appName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": appName,
		"deployer.io/app":        appName,
	}
}

func placementArchitecture(placement string) string {
	parts := strings.Split(strings.TrimSpace(placement), "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return strings.TrimSpace(placement)
}

func placementLabelKey(key string) string {
	switch strings.TrimSpace(key) {
	case "arch":
		return "kubernetes.io/arch"
	case "location", "role", "node-id":
		return "deployer.io/" + strings.TrimSpace(key)
	default:
		return strings.TrimSpace(key)
	}
}
