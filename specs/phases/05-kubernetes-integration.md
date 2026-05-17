# Phase 05: Kubernetes Integration

Goal: map app desired state to k3s/Kubernetes objects.

## 05.01 Add Kubernetes Client Configuration

Goal: let the control plane talk to the local k3s API.

Inputs:

- Server config.
- k3s expected on VPS.

Implementation Notes:

- Support kubeconfig path from config.
- Default to common k3s kubeconfig path if appropriate.
- Add readiness check for Kubernetes connectivity.

Acceptance Criteria:

- Server can load kubeconfig.
- Connectivity failure is reported clearly.
- `/readyz` can include Kubernetes readiness if enabled.

Dependencies:

- `01.01`
- `00.05`

Out Of Scope:

- Agent-side Kubernetes apply.

## 05.02 Add Kubernetes Namespace Strategy

Goal: decide where app resources live.

Inputs:

- App naming model.

Implementation Notes:

- MVP can use one namespace, e.g. `deployer-apps`.
- Ensure namespace exists before applying resources.

Acceptance Criteria:

- Namespace creation is idempotent.
- Tests cover name generation where possible.

Dependencies:

- `05.01`

Out Of Scope:

- Per-app namespaces.

## 05.03 Generate Deployment Manifest

Goal: convert app desired state into a Kubernetes Deployment.

Inputs:

- App model.
- App config validation.

Implementation Notes:

- Set:
  - name
  - labels
  - image
  - container port
  - replica count
  - readiness probe
  - liveness probe if configured
  - node selector for `kubernetes.io/arch=arm64`
- Include topology spread if `placement.spread=true`.

Acceptance Criteria:

- Generated Deployment is deterministic.
- Unit tests assert key fields.
- Names are Kubernetes-safe.

Dependencies:

- `04.01`

Out Of Scope:

- StatefulSets.

## 05.04 Generate Service Manifest

Goal: expose pods inside the cluster.

Inputs:

- App model.

Implementation Notes:

- Generate ClusterIP Service.
- Map service port to container port.
- Use stable labels.

Acceptance Criteria:

- Generated Service selector matches Deployment pod labels.
- Unit tests assert port mapping.

Dependencies:

- `05.03`

Out Of Scope:

- LoadBalancer services.

## 05.05 Apply App Resources

Goal: apply generated Deployment and Service to Kubernetes.

Inputs:

- Kubernetes client.
- Manifest generation.

Implementation Notes:

- Use Kubernetes API clients, not shelling out to `kubectl`.
- Create or patch existing objects.
- Use server-side apply if practical.

Acceptance Criteria:

- Applying an app creates Deployment and Service.
- Re-applying is idempotent.
- Updating image changes Deployment.

Dependencies:

- `05.01`
- `05.02`
- `05.03`
- `05.04`

Out Of Scope:

- Ingress.

## 05.06 Wire Deploy API To Kubernetes Apply

Goal: make desired state changes create real workloads.

Inputs:

- App API.
- Apply logic.
- Deployment records.

Implementation Notes:

- On app create/update, create deployment record.
- Apply resources.
- Mark deployment applying/healthy/failed based on immediate result.
- Later tasks can improve rollout watching.

Acceptance Criteria:

- `deployer deploy` results in Kubernetes resources.
- API returns apply failure when Kubernetes apply fails.
- Deployment record captures failure reason.

Dependencies:

- `04.04`
- `04.07`
- `05.05`

Out Of Scope:

- Streaming rollout status.

## 05.07 Add Rollout Status Reader

Goal: report whether app replicas are healthy.

Inputs:

- Kubernetes client.
- App Deployment.

Implementation Notes:

- Read Deployment status:
  - desired replicas
  - updated replicas
  - available replicas
  - conditions
- Map to platform status.

Acceptance Criteria:

- API can return app runtime status.
- CLI can display healthy/desired counts.
- Handles Deployment not found gracefully.

Dependencies:

- `05.06`

Out Of Scope:

- Watch-based streaming.

## 05.08 Add Logs Command Via Kubernetes

Goal: let operators view app pod logs.

Inputs:

- Kubernetes client.
- CLI.
- gRPC streaming support.

Implementation Notes:

- Server-mediated streaming RPC.
- CLI:
  - `deployer logs <app>`
  - `--follow`
  - `--tail`
- Select pods by app label.

Acceptance Criteria:

- Logs command prints recent logs.
- Follow mode streams until interrupted.
- Missing app gives clear error.

Dependencies:

- `05.07`
- `02.04`

Out Of Scope:

- Long-term log storage.
