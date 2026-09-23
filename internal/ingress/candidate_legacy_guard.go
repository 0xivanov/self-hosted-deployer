package ingress

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ErrCandidateManagedApp = errors.New("app uses isolated candidate deployments; legacy mutation is disabled")

func candidateManagedService(service *corev1.Service) bool {
	_, activation := service.Annotations[activationOperationAnnotation]
	_, bootstrap := service.Annotations[initialCandidateRequestAnnotation]
	_, generation := service.Spec.Selector[candidateGenerationLabel]
	return activation || bootstrap || generation
}

// rejectLegacyCandidateMutation prevents new legacy work after opt-in. It does
// not drain already-running calls in an older server process; rollout still
// requires stopping those writers before creating a candidate activation gate.
func (c *Controller) rejectLegacyCandidateMutation(ctx context.Context, app string) error {
	if c.services == nil {
		return errors.New("Service client required for app mutation")
	}
	service, err := c.services.Get(ctx, app, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = requireAppResourceOwnership("Service", app, app, service.Labels); err != nil {
		return err
	}
	if candidateManagedService(service) {
		return ErrCandidateManagedApp
	}
	return nil
}
