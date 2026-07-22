package ingress

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

func (c *Controller) reconcileTrafficResilienceResources(ctx context.Context, cfg appconfig.Config) error {
	if c.middlewares == nil || c.serverTransports == nil {
		return nil
	}
	if strings.TrimSpace(cfg.Routing.Domain) == "" {
		return c.deleteTrafficResilienceResources(ctx, cfg.Name)
	}
	if err := reconcileDynamicAppResource(
		ctx,
		c.middlewares,
		retryMiddlewareForApp(cfg, c.namespace),
		"Middleware",
	); err != nil {
		return err
	}
	return reconcileDynamicAppResource(
		ctx,
		c.serverTransports,
		serversTransportForApp(cfg, c.namespace),
		"ServersTransport",
	)
}

func (c *Controller) deleteTrafficResilienceResources(ctx context.Context, appName string) error {
	var errs []error
	if c.middlewares != nil {
		errs = append(errs, deleteDynamicAppResource(ctx, c.middlewares, appName, "Middleware"))
	}
	if c.serverTransports != nil {
		errs = append(errs, deleteDynamicAppResource(ctx, c.serverTransports, appName, "ServersTransport"))
	}
	return errors.Join(errs...)
}

func retryMiddlewareForApp(cfg appconfig.Config, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "Middleware",
		"metadata": map[string]any{
			"name":      cfg.Name,
			"namespace": namespace,
			"labels":    stringMapToAnyMap(managedAppLabels(cfg.Name)),
		},
		"spec": map[string]any{
			"retry": map[string]any{
				"attempts":        int64(2),
				"initialInterval": "100ms",
			},
		},
	}}
}

func serversTransportForApp(cfg appconfig.Config, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "ServersTransport",
		"metadata": map[string]any{
			"name":      cfg.Name,
			"namespace": namespace,
			"labels":    stringMapToAnyMap(managedAppLabels(cfg.Name)),
		},
		"spec": map[string]any{
			"forwardingTimeouts": map[string]any{
				"dialTimeout": "2s",
			},
		},
	}}
}

func reconcileDynamicAppResource(
	ctx context.Context,
	resources dynamic.ResourceInterface,
	desired *unstructured.Unstructured,
	kind string,
) error {
	existing, err := resources.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := resources.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create %s %q: %w", kind, desired.GetName(), err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get %s %q: %w", kind, desired.GetName(), err)
	}
	if err := requireAppResourceOwnership(kind, existing.GetName(), desired.GetName(), existing.GetLabels()); err != nil {
		return err
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	if _, err := resources.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update %s %q: %w", kind, desired.GetName(), err)
	}
	return nil
}

func deleteDynamicAppResource(
	ctx context.Context,
	resources dynamic.ResourceInterface,
	appName string,
	kind string,
) error {
	existing, err := resources.Get(ctx, appName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get %s %q for deletion: %w", kind, appName, err)
	}
	if err := requireAppResourceOwnership(kind, existing.GetName(), appName, existing.GetLabels()); err != nil {
		return err
	}
	if err := resources.Delete(ctx, appName, ownedDeleteOptions(existing)); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete %s %q: %w", kind, appName, err)
	}
	return nil
}

func stringMapToAnyMap(values map[string]string) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
