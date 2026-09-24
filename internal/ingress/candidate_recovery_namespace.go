package ingress

import (
	"context"
	"errors"
)

// EnsureCandidateRecoveryNamespace permits passive recovery resources when
// initial preparation stopped before namespace creation. It neither admits
// workload capacity nor creates secrets, pods, routes or active services.
func (c *Controller) EnsureCandidateRecoveryNamespace(ctx context.Context) error {
	if c.namespaces == nil {
		return errors.New("candidate recovery namespace client is unavailable")
	}
	return c.reconcileNamespace(ctx)
}
