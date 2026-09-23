package ingress

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReconcileCandidateRoute verifies the already-fenced Service target before
// changing only the app-owned Ingress. It does not fence unrelated writers;
// callers must hold the app mutation lock and ensure old writers have drained.
func (c *Controller) ReconcileCandidateRoute(ctx context.Context, gate ActivationGate, target ActivationTarget, cfg appconfig.Config) error {
	if c.services == nil || c.ingresses == nil {
		return errors.New("candidate route runtime is unavailable")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if gate.App != cfg.Name || !registryauth.ValidRevision(gate.OperationID) || len(target.Selector) == 0 || len(target.Ports) == 0 {
		return errors.New("invalid candidate route identity")
	}
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return err
	}
	if service.DeletionTimestamp != nil {
		return errors.New("activation Service is being deleted")
	}
	if !apiequality.Semantic.DeepEqual(service.Spec.Selector, target.Selector) || !apiequality.Semantic.DeepEqual(service.Spec.Ports, target.Ports) {
		return errors.New("candidate route target is not selected by the fenced Service")
	}
	expected := serviceForApp(cfg, c.namespace)
	if !apiequality.Semantic.DeepEqual(service.Spec.Ports, expected.Spec.Ports) {
		return errors.New("candidate route port does not match configuration")
	}
	manifest, ok, err := ManifestForApp(cfg, c.namespace, c.tls)
	if err != nil {
		return err
	}
	if !ok {
		existing, err := c.ingresses.Get(ctx, cfg.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read app Ingress for deletion: %w", err)
		}
		if err := requireAppResourceOwnership("Ingress", existing.Name, cfg.Name, existing.Labels); err != nil {
			return err
		}
		if existing.UID == "" || existing.ResourceVersion == "" || existing.DeletionTimestamp != nil {
			return errors.New("app Ingress lacks a stable deletion identity")
		}
		if err := c.ingresses.Delete(ctx, existing.Name, ownedDeleteOptionsWithResourceVersion(existing)); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete app Ingress: %w", err)
		}
		return nil
	}
	desired := kubernetesIngress(manifest)
	existing, err := c.ingresses.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.ingresses.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create app Ingress: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read app Ingress: %w", err)
	}
	if err := requireAppResourceOwnership("Ingress", existing.Name, cfg.Name, existing.Labels); err != nil {
		return err
	}
	if existing.UID == "" || existing.ResourceVersion == "" || existing.DeletionTimestamp != nil {
		return errors.New("app Ingress lacks a stable update identity")
	}
	desired.UID = existing.UID
	desired.ResourceVersion = existing.ResourceVersion
	if _, err := c.ingresses.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update app Ingress: %w", err)
	}
	return nil
}
