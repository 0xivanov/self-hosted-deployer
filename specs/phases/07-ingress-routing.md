# Phase 07: Ingress And Routing

Goal: expose apps publicly through the VPS and route only to healthy services.

## 07.01 Choose MVP Routing Backend

Goal: decide whether to use Kubernetes Ingress, Caddy, or Traefik first.

Inputs:

- k3s integration.
- VPS public edge requirement.

Implementation Notes:

- Preferred MVP option: Kubernetes Ingress with Traefik if using k3s defaults.
- Alternative: manage Caddy outside Kubernetes for simpler public proxy config.
- Document the decision in `PRODUCT_SPEC.md` or an ADR.

Acceptance Criteria:

- Routing backend decision is documented.
- Tradeoffs are captured.

Dependencies:

- `05.01`

Out Of Scope:

- Implementing routing.

## 07.02 Generate Ingress Manifest

Goal: map app domain to Kubernetes Service.

Inputs:

- Routing backend decision.
- App domain.
- Service manifest.

Implementation Notes:

- Generate Ingress for apps with `routing.domain`.
- Attach TLS annotations only after TLS strategy is chosen.
- Route `/` to app service.

Acceptance Criteria:

- Generated Ingress points to correct Service.
- Unit tests assert host and backend.

Dependencies:

- `05.04`
- `07.01`

Out Of Scope:

- Multiple paths per app.

## 07.03 Apply Ingress With App Resources

Goal: make deployed apps reachable by domain.

Inputs:

- Ingress generation.
- Kubernetes apply.

Implementation Notes:

- Apply Ingress after Service exists.
- Delete or update Ingress when domain changes.

Acceptance Criteria:

- App with domain gets Ingress.
- App without domain skips Ingress.
- Updating domain updates Ingress.

Dependencies:

- `07.02`
- `05.05`

Out Of Scope:

- Automatic DNS management.

## 07.04 Add TLS Strategy

Goal: define and implement HTTPS.

Inputs:

- Routing backend.

Implementation Notes:

- If Traefik: use cert-manager or Traefik ACME.
- If Caddy: use Caddy automatic HTTPS.
- Store email/contact config.

Acceptance Criteria:

- TLS config is documented.
- App domain can be served over HTTPS in a real environment.
- HTTP to HTTPS behavior is defined.

Dependencies:

- `07.03`

Out Of Scope:

- Wildcard certificate automation.

## 07.05 Add Route Store And API

Goal: track public routes at platform level.

Inputs:

- App routing config.

Implementation Notes:

- Store route:
  - app
  - domain
  - status
  - TLS enabled
- API:
  - list routes
  - inspect route

Acceptance Criteria:

- Route is created when app has domain.
- Route updates when domain changes.
- CLI can list routes.

Dependencies:

- `04.03`
- `07.03`

Out Of Scope:

- User-created routes independent of apps.

## 07.06 Add Basic Health-Aware Runtime Status

Goal: expose whether routes have healthy backend replicas.

Inputs:

- Kubernetes rollout status.
- Route store.

Implementation Notes:

- Derive health from Deployment available replicas.
- Show route status:
  - healthy
  - degraded
  - unavailable

Acceptance Criteria:

- Route status changes when app has zero available replicas.
- CLI route list shows health.

Dependencies:

- `05.07`
- `07.05`

Out Of Scope:

- Active HTTP health checks by proxy.
