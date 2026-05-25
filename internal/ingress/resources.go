package ingress

import (
	"context"
	"fmt"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func (c *Controller) reconcileAppResources(ctx context.Context, cfg appconfig.Config) error {
	if err := c.reconcileNamespace(ctx); err != nil {
		return err
	}
	if err := c.reconcileDeployment(ctx, cfg); err != nil {
		return err
	}
	if err := c.reconcileService(ctx, cfg); err != nil {
		return err
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

func (c *Controller) reconcileDeployment(ctx context.Context, cfg appconfig.Config) error {
	desired := deploymentForApp(cfg, c.namespace)
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

func deploymentForApp(cfg appconfig.Config, namespace string) *appsv1.Deployment {
	cfg.Normalize()
	labels := appLabels(cfg.Name)
	replicas := int32(cfg.Deploy.Replicas)
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
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
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
	if cfg.Placement.Spread {
		deployment.Spec.Template.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
			MaxSkew:           1,
			TopologyKey:       "kubernetes.io/hostname",
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     &metav1.LabelSelector{MatchLabels: labels},
		}}
	}
	return deployment
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
