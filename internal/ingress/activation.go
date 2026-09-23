package ingress

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const activationOperationAnnotation = "deployer.io/activation-operation"

var ErrActivationSuperseded = errors.New("activation gate changed; do not replay this operation")

// ActivationGate is a durable, single-use compare-and-swap token. Persist the
// captured token before dispatch. Never capture a fresh token to retry an old
// operation. These primitives do not exclude legacy service writers by themselves.
type ActivationGate struct {
	App             string    `json:"app"`
	Namespace       string    `json:"namespace"`
	UID             types.UID `json:"uid"`
	ResourceVersion string    `json:"resource_version"`
	OperationID     string    `json:"operation_id,omitempty"`
}

type ActivationTarget struct {
	Selector map[string]string    `json:"selector"`
	Ports    []corev1.ServicePort `json:"ports"`
}

func activationGateFor(service *corev1.Service) ActivationGate {
	return ActivationGate{App: service.Name, Namespace: service.Namespace, UID: service.UID, ResourceVersion: service.ResourceVersion, OperationID: service.Annotations[activationOperationAnnotation]}
}

// CaptureActivationGate is read-only. Initial service creation and the durable
// association with a deployment request belong to the dispatch coordinator.
func (c *Controller) CaptureActivationGate(ctx context.Context, app string) (ActivationGate, ActivationTarget, error) {
	service, err := c.services.Get(ctx, app, metav1.GetOptions{})
	if err != nil {
		return ActivationGate{}, ActivationTarget{}, err
	}
	if err = requireAppResourceOwnership("Service", app, app, service.Labels); err != nil {
		return ActivationGate{}, ActivationTarget{}, err
	}
	if service.UID == "" || service.ResourceVersion == "" {
		return ActivationGate{}, ActivationTarget{}, errors.New("service lacks activation identity")
	}
	copy := service.DeepCopy()
	return activationGateFor(service), ActivationTarget{Selector: copy.Spec.Selector, Ports: copy.Spec.Ports}, nil
}

func (c *Controller) serviceAtGate(ctx context.Context, gate ActivationGate) (*corev1.Service, error) {
	if gate.App == "" || gate.Namespace != c.namespace || gate.UID == "" || gate.ResourceVersion == "" {
		return nil, ErrActivationSuperseded
	}
	service, err := c.services.Get(ctx, gate.App, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, ErrActivationSuperseded
	}
	if err != nil {
		return nil, err
	}
	if err = requireAppResourceOwnership("Service", service.Name, gate.App, service.Labels); err != nil {
		return nil, err
	}
	if activationGateFor(service) != gate {
		return nil, ErrActivationSuperseded
	}
	return service.DeepCopy(), nil
}

func (c *Controller) updateActivationGate(ctx context.Context, service *corev1.Service) (ActivationGate, error) {
	// The API server enforces resourceVersion atomically, including a writer
	// racing between the preceding GET and this UPDATE. No conflict retry.
	updated, err := c.services.Update(ctx, service, metav1.UpdateOptions{})
	if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
		return ActivationGate{}, ErrActivationSuperseded
	}
	if err != nil {
		return ActivationGate{}, err
	}
	return activationGateFor(updated), nil
}

// FenceActivationGate invalidates older activation tokens while preserving the
// current endpoint selection. The returned token must be durably recorded.
func (c *Controller) FenceActivationGate(ctx context.Context, gate ActivationGate, operationID string) (ActivationGate, error) {
	if !registryauth.ValidRevision(operationID) || operationID == gate.OperationID {
		return ActivationGate{}, fmt.Errorf("a new activation operation ID is required")
	}
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return ActivationGate{}, err
	}
	if service.Annotations == nil {
		service.Annotations = map[string]string{}
	}
	service.Annotations[activationOperationAnnotation] = operationID
	return c.updateActivationGate(ctx, service)
}

// ReplaceActivationTarget selects a prepared generation or a captured predecessor.
// It must only receive a validated target from the trusted coordinator, never
// browser input. Changing selector and ports in one service update prevents a
// candidate from changing one while a recovery changes the other.
func (c *Controller) ReplaceActivationTarget(ctx context.Context, gate ActivationGate, target ActivationTarget) (ActivationGate, error) {
	if !registryauth.ValidRevision(gate.OperationID) || len(target.Selector) == 0 || len(target.Ports) == 0 {
		return ActivationGate{}, errors.New("invalid activation target")
	}
	service, err := c.serviceAtGate(ctx, gate)
	if err != nil {
		return ActivationGate{}, err
	}
	service.Spec.Selector = maps.Clone(target.Selector)
	service.Spec.Ports = append([]corev1.ServicePort(nil), target.Ports...)
	return c.updateActivationGate(ctx, service)
}
