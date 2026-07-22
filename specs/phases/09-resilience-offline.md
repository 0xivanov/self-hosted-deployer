# Phase 09: Resilience And Offline Behavior

Goal: make unreliable nodes visible and keep stateless apps reachable when possible.

## 09.01 Add Node Offline Detector

Goal: mark nodes offline when heartbeats stop.

Inputs:

- Heartbeat timestamps.

Implementation Notes:

- Background loop in control plane.
- Configurable offline threshold.
- Status transition:
  - online -> offline
- Do not remove node identity.

Acceptance Criteria:

- Node becomes offline after threshold.
- Node returns online after heartbeat.
- Status transitions are logged.

Dependencies:

- `03.05`

Out Of Scope:

- Alerts.

## 09.02 Sync Kubernetes Node Status

Goal: compare platform node state with Kubernetes node readiness.

Inputs:

- Kubernetes client.
- Node model.

Implementation Notes:

- Read Kubernetes Node objects.
- Map k8s readiness into platform node inspect output.
- Do not rely only on agent heartbeat.

Acceptance Criteria:

- CLI node inspect shows agent status and k8s readiness.
- Missing Kubernetes node is handled clearly.

Dependencies:

- `05.01`
- `03.08`

Out Of Scope:

- Automatic node deletion.

## 09.03 Add Node Drain Command

Goal: prevent new workloads from landing on a node and evict movable workloads.

Inputs:

- Node model.
- Kubernetes client.

Implementation Notes:

- CLI:
  - `deployer nodes drain <node>`
- Platform behavior:
  - mark node drained
  - cordon Kubernetes node
  - optionally evict pods respecting safety settings

Acceptance Criteria:

- Drained node does not receive new pods.
- CLI list shows drained status.
- Command is idempotent.

Dependencies:

- `03.08`
- `05.01`

Out Of Scope:

- Draining stateful pinned workloads automatically.

## 09.04 Add Node Uncordon Command

Goal: return a drained node to scheduling.

Inputs:

- Drain command.

Implementation Notes:

- CLI:
  - `deployer nodes uncordon <node>`
- Mark node schedulable.
- Uncordon Kubernetes node.

Acceptance Criteria:

- Node returns to online/schedulable if healthy.
- Command is idempotent.

Dependencies:

- `09.03`

Out Of Scope:

- Automatic capacity rebalancing.

## 09.05 Add Node Remove Command

Goal: revoke a node from the platform.

Inputs:

- Node identity.
- WireGuard peer config.
- Kubernetes node model.

Implementation Notes:

- CLI:
  - `deployer nodes remove <node>`
- Confirm unless `--yes`.
- Revoke agent token.
- Disable WireGuard peer.
- Mark node removed.
- Optionally delete Kubernetes node object when safe.

Acceptance Criteria:

- Removed node cannot heartbeat.
- Removed node is excluded from WireGuard config.
- Removed node is excluded from scheduling.

Dependencies:

- `03.04`
- `06.04`

Out Of Scope:

- Remote uninstall.

## 09.06 Add Resilience Mode To App Config

Goal: expose simple operator-facing app availability modes.

Inputs:

- App config schema.

Implementation Notes:

- Add:
  - `resilience.mode`
- Supported values:
  - `basic`
  - `resilient`
  - `fallback`
  - `pinned`
- Validate against state mode.

Acceptance Criteria:

- Invalid mode rejected.
- Stateful apps cannot use unsafe automatic failover without explicit override.

Dependencies:

- `04.01`
- `04.02`

Out Of Scope:

- Complex policy language.

## 09.07 Map Resilience Modes To Kubernetes Policy

Goal: generate safe scheduling behavior from simple modes.

Inputs:

- Resilience mode.
- Kubernetes manifest generation.

Implementation Notes:

- `basic`: one or configured replicas.
- `resilient`: replicas >= 2, zero unavailable replicas during rollout, one
  surge replica, revision-aware topology spread, and a stable-readiness window.
- Routed apps: bounded backend dial time plus retry on network failure.
- `fallback`: prefer home nodes, allow VPS fallback.
- `pinned`: node selector for selected node.

Acceptance Criteria:

- Generated Deployment differs by mode as documented.
- Unit tests cover each mode.

Dependencies:

- `09.06`
- `05.03`

Out Of Scope:

- Dynamic autoscaling.

## 09.08 Add App Runtime Status Command

Goal: show whether resilience target is being met.

Inputs:

- Rollout status reader.
- Resilience mode.

Implementation Notes:

- Command:
  - `deployer status <app>`
- Show:
  - desired replicas
  - available replicas
  - nodes running replicas
  - route health
  - warnings if spread/fallback not satisfied

Acceptance Criteria:

- Status is useful during node outage.
- JSON output includes machine-readable health fields.

Dependencies:

- `05.07`
- `07.06`
- `09.07`

Out Of Scope:

- Historical uptime.
