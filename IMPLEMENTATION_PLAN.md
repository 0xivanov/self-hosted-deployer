# Implementation Plan

This document indexes the detailed implementation task specs. The task size is intentionally smaller than a typical scrum story: each item should be implementable, reviewable, and testable on its own.

The main product spec remains the north star:

- [PRODUCT_SPEC.md](PRODUCT_SPEC.md)

## Delivery Strategy

Build the platform as a thin vertical slice first, then deepen each subsystem. The CLI must be implemented in Go. The server and agent should also be implemented in Go unless a later design decision explicitly changes that.

The preferred order is:

1. Create a local Go monorepo skeleton.
2. Implement config, logging, and shared API contracts.
3. Build the control plane API with local persistence.
4. Build the CLI against the control plane.
5. Add agent registration and heartbeats.
6. Add app desired-state storage.
7. Add WireGuard automation.
8. Bootstrap the k3s server and join worker nodes.
9. Add Kubernetes manifest generation and apply.
10. Add ingress/routing.
11. Add secrets.
12. Add platform events and diagnostics.
13. Add resilience/offline behavior.
14. Package and harden.
15. Add GitHub Releases and automated platform updates.

## Phase Index

- [Phase 00: Project Foundations](specs/phases/00-project-foundations.md)
- [Phase 01: Control Plane Core](specs/phases/01-control-plane-core.md)
- [Phase 02: CLI Core](specs/phases/02-cli-core.md)
- [Phase 03: Agent Core](specs/phases/03-agent-core.md)
- [Phase 04: App Desired State](specs/phases/04-app-desired-state.md)
- [Phase 05: Kubernetes Integration](specs/phases/05-kubernetes-integration.md)
- [Phase 06: WireGuard Networking](specs/phases/06-wireguard-networking.md)
- [Phase 07: Ingress And Routing](specs/phases/07-ingress-routing.md)
- [Phase 08: Secrets Management](specs/phases/08-secrets-management.md)
- [Phase 08.5: Events And Diagnostics](specs/phases/09-events-diagnostics.md)
- [Phase 09: Resilience And Offline Behavior](specs/phases/09-resilience-offline.md)
- [Phase 10: Packaging And Operations](specs/phases/10-packaging-operations.md)
- [Phase 11: Releases And Automated Updates](specs/phases/11-releases-updates.md)

## Task Spec Template

Each task uses this shape:

```text
Task ID
Goal
Inputs
Implementation Notes
Acceptance Criteria
Dependencies
Out Of Scope
```

## Definition Of Done For Every Task

- Code is formatted.
- Unit tests are added when behavior is non-trivial.
- CLI/API behavior has at least one integration-style test or documented manual check.
- Error messages are operator-friendly.
- No plaintext secret values are logged.
- Existing behavior is not broken.
- Documentation or command help is updated when user-facing behavior changes.

## MVP Completion Target

The MVP is complete when this scenario works:

1. A VPS runs the control plane, k3s server, WireGuard hub, and ingress.
2. Two ARM64 Linux nodes join using generated install commands.
3. A stateless app deploys with two replicas.
4. `api.example.com` routes through the VPS to healthy replicas.
5. One node goes offline.
6. The platform marks it offline, removes it from routing, and keeps the app reachable.
7. The node returns and reconciles without manual SSH intervention.
