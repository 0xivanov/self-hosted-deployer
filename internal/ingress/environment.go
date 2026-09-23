package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const environmentSecretLabel = "deployer.io/environment-bundle"
const environmentRevisionAnnotation = "deployer.io/environment-revision"

func environmentSecretName(app, revision string) string {
	sum := sha256.Sum256([]byte(app + "\x00" + revision))
	return app + "-env-" + hex.EncodeToString(sum[:])[:24]
}

// Check before resource writes. Environment revisions are immutable snapshots,
// not aliases for the mutable legacy app Secret.
func validateEnvironmentReference(cfg appconfig.Config, values map[string]string, revision string) error {
	if cfg.EnvironmentRevision == "" {
		return nil
	}
	if !appconfig.ValidAppName(cfg.Name) || !registryauth.ValidRevision(cfg.EnvironmentRevision) || revision != cfg.EnvironmentRevision || len(cfg.Secrets) > 0 || len(values) > 64 {
		return errors.New("invalid environment revision")
	}
	total := 0
	for name, value := range values {
		if appconfig.ValidateSecretName(name) != nil || len(name) > 128 || len(value) > 8192 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return errors.New("invalid environment values")
		}
		total += len(name) + len(value)
	}
	if total > 32768 {
		return errors.New("invalid environment size")
	}
	return nil
}

func (c *Controller) reconcileEnvironmentSecret(ctx context.Context, cfg appconfig.Config, values map[string]string) error {
	name := environmentSecretName(cfg.Name, cfg.EnvironmentRevision)
	labels := managedAppLabels(cfg.Name)
	labels[environmentSecretLabel] = "true"
	data := make(map[string][]byte, len(values))
	for key, value := range values {
		data[key] = []byte(value)
	}
	desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.namespace, Labels: labels, Annotations: map[string]string{environmentRevisionAnnotation: cfg.EnvironmentRevision}}, Type: corev1.SecretTypeOpaque, Immutable: boolPtr(true), Data: data}
	existing, err := c.appSecrets.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err = c.appSecrets.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return errors.New("create environment Secret failed")
		}
		return nil
	}
	if err != nil {
		return errors.New("read environment Secret failed")
	}
	if err = requireAppResourceOwnership("Secret", name, cfg.Name, existing.Labels); err != nil {
		return err
	}
	if existing.Labels[managedByLabel] != managedByDeployer || existing.Labels[environmentSecretLabel] != "true" || existing.Annotations[environmentRevisionAnnotation] != cfg.EnvironmentRevision || existing.Type != corev1.SecretTypeOpaque || existing.Immutable == nil || !*existing.Immutable || !jsonEqual(existing.Data, data) {
		return errors.New("environment Secret does not match immutable revision")
	}
	return nil
}

func (c *Controller) deleteEnvironmentSecrets(ctx context.Context, app string) error {
	secrets, err := c.appSecrets.List(ctx, metav1.ListOptions{LabelSelector: appOwnershipLabel + "=" + app + "," + environmentSecretLabel + "=true"})
	if err != nil {
		return errors.New("list environment Secrets failed")
	}
	var errs []error
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		revision := secret.Annotations[environmentRevisionAnnotation]
		if err := requireAppResourceOwnership("Secret", secret.Name, app, secret.Labels); err != nil {
			errs = append(errs, err)
			continue
		}
		if secret.Labels[managedByLabel] != managedByDeployer || secret.Labels[environmentSecretLabel] != "true" || !registryauth.ValidRevision(revision) || secret.Name != environmentSecretName(app, revision) || secret.Type != corev1.SecretTypeOpaque || secret.Immutable == nil || !*secret.Immutable {
			errs = append(errs, fmt.Errorf("environment Secret %q is not a managed immutable revision", secret.Name))
			continue
		}
		if err := c.appSecrets.Delete(ctx, secret.Name, ownedDeleteOptionsWithResourceVersion(secret)); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, errors.New("delete environment Secret failed"))
		}
	}
	return errors.Join(errs...)
}
