# Phase 05: Kubernetes Bootstrap And Integration

Goal: bootstrap the MVP k3s cluster, join worker nodes over WireGuard, then map app desired state to Kubernetes objects.

Execution order note: the bootstrap and worker-join tasks in this phase depend on WireGuard connectivity from Phase 06. Even though this file remains Phase 05 to avoid renumbering the plan, implement WireGuard before running the k3s worker-join tasks.

## 05.01 Define k3s Cluster Bootstrap Strategy

Goal: document how the platform creates and manages the single MVP k3s cluster.

Inputs:

- VPS control-plane architecture.
- WireGuard private network.
- ARM64-first worker-node requirement.

Implementation Notes:

- MVP topology:
  - VPS runs k3s server/control-plane.
  - Home/relative nodes run k3s agents/workers.
- k3s API should be reachable by workers over WireGuard.
- Prefer binding/advertising the k3s server on the VPS WireGuard IP.
- Define whether the deployer installs k3s directly or invokes the official k3s installer.
- Define where k3s server config is written.
- Define how the deployer detects an existing k3s install.

Acceptance Criteria:

- Bootstrap strategy is documented in this phase.
- Required k3s server flags/config are listed.
- Existing-install detection behavior is specified.

Bootstrap Strategy:

- The MVP runs one k3s server on the VPS and zero or more k3s agents on enrolled Linux worker nodes.
- k3s is installed through the official `get.k3s.io` installer rather than by vendoring a k3s binary. The deployer writes the server configuration first, then invokes the installer with `INSTALL_K3S_EXEC="server --config <path>"`.
- The k3s server config is written to `/etc/rancher/k3s/config.yaml` by default. Operators can override this with `DEPLOYER_K3S_CONFIG_PATH`.
- The control-plane kubeconfig defaults to `/etc/rancher/k3s/k3s.yaml`. Operators can override this with `DEPLOYER_KUBECONFIG`.
- The k3s API is bound and advertised on the VPS WireGuard hub IP. Operators provide that IP through `DEPLOYER_K3S_WIREGUARD_IP` until Phase 06 persists hub network state.
- Required k3s server config fields:
  - `bind-address: <wireguard-ip>`
  - `advertise-address: <wireguard-ip>`
  - `node-ip: <wireguard-ip>`
  - `tls-san: [<wireguard-ip>]`
  - `write-kubeconfig: <kubeconfig-path>`
  - `write-kubeconfig-mode: "0644"`
- Existing install detection is conservative:
  - If a k3s binary is already present or the configured kubeconfig already exists, `deployer-server bootstrap k3s` fails safely by default and tells the operator to inspect the host.
  - Re-applying k3s config or re-running the installer requires an explicit `--force` flag.
  - The bootstrap command never prints k3s node tokens.

Dependencies:

- `06.06`

Out Of Scope:

- Highly available k3s control plane.
- Multi-cluster support.

## 05.02 Bootstrap k3s Server On VPS

Goal: install and start the k3s server/control-plane on the VPS.

Inputs:

- Server binary and config.
- WireGuard hub configuration.

Implementation Notes:

- Extend `deployer-server bootstrap` or add a dedicated subcommand:
  - `deployer-server bootstrap k3s`
- Validate prerequisites:
  - Linux host
  - root privileges or clear sudo requirement
  - WireGuard hub IP is configured
  - required ports are available
- Install/start k3s server.
- Configure k3s to advertise the VPS WireGuard IP to workers.
- Store/read kubeconfig for control-plane use.
- Do not overwrite an existing k3s installation without explicit confirmation.

Acceptance Criteria:

- Fresh VPS can bootstrap a single-node k3s server.
- k3s API is reachable locally.
- k3s API is reachable on the WireGuard IP.
- Kubeconfig path is recorded in server config.
- Re-running bootstrap is idempotent or fails safely with clear guidance.

Dependencies:

- `06.04`
- `01.01`
- `00.05`

Out Of Scope:

- Installing Kubernetes distributions other than k3s.

## 05.03 Manage k3s Worker Join Token

Goal: make the k3s worker join token available to authorized agents without exposing it broadly.

Inputs:

- k3s server bootstrap.
- Agent authentication model.

Implementation Notes:

- Read the k3s node-token from the VPS k3s server.
- Store it encrypted or read it on demand from the k3s server filesystem.
- Expose it only to authenticated agents that are approved to join the cluster.
- Never display the k3s token in normal CLI output.
- Never log the k3s token.
- Track when a node has requested or used worker join material.

Acceptance Criteria:

- Control plane can obtain k3s worker join material.
- Agent can request join material only after node enrollment.
- Admin token is required for any operator-facing token inspection or rotation.
- k3s token is redacted in logs and events.

Dependencies:

- `05.02`
- `03.04`
- `08.02`

Out Of Scope:

- Automatic k3s token rotation.

## 05.04 Add Agent k3s Worker Join Command

Goal: let an enrolled node join the k3s cluster as a worker after WireGuard is connected.

Inputs:

- Agent identity.
- WireGuard interface.
- k3s worker join material.

Implementation Notes:

- Add agent command or run-loop step:
  - `deployer-agent join-k3s`
- Ensure WireGuard connectivity is healthy before joining.
- Use the VPS WireGuard IP as `K3S_URL`.
- Use k3s worker token from the control plane.
- Install/start k3s agent on the node.
- Set a stable Kubernetes node name that maps to the deployer node name.
- Make the operation idempotent.

Acceptance Criteria:

- Fresh ARM64 Linux worker can join the VPS k3s server.
- Joined node appears in Kubernetes as Ready.
- Re-running join does not create duplicate nodes.
- Join fails clearly when WireGuard is disconnected.

Dependencies:

- `03.07`
- `05.03`
- `06.07`

Out Of Scope:

- Joining Windows or non-Linux nodes.

## 05.05 Label And Taint Kubernetes Nodes

Goal: make Kubernetes scheduling aware of deployer node metadata.

Inputs:

- Deployer node labels.
- Kubernetes client.

Implementation Notes:

- Apply labels such as:
  - `deployer.io/node-id`
  - `deployer.io/location`
  - `deployer.io/role`
  - `kubernetes.io/arch`
- Apply taints for drained or restricted nodes later.
- Keep Kubernetes node labels in sync when deployer node metadata changes.

Acceptance Criteria:

- Worker nodes have deployer labels after join.
- ARM64 worker nodes are labeled for architecture-aware scheduling.
- Label sync is idempotent.

Dependencies:

- `05.04`
- `03.01`

Out Of Scope:

- Complex placement policy language.

## 05.06 Verify Cluster Worker Readiness

Goal: block app scheduling until the cluster has healthy worker capacity.

Inputs:

- Kubernetes client.
- Node status model.

Implementation Notes:

- Check that expected worker nodes exist in Kubernetes.
- Check `Ready` condition.
- Check architecture labels.
- Report readiness in:
  - `deployer nodes inspect`
  - `deployer doctor`
  - server readiness diagnostics

Acceptance Criteria:

- Platform can distinguish enrolled-but-not-joined nodes from Kubernetes-ready workers.
- App deploy can fail early when no schedulable workers exist.
- Node inspect shows k3s join/readiness state.

Dependencies:

- `05.05`
- `03.08`

Out Of Scope:

- Capacity planning beyond basic readiness.

## 05.07 Add Kubernetes Client Configuration

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

- `05.02`
- `05.06`
- `00.05`

Out Of Scope:

- Agent-side Kubernetes apply.

## 05.08 Add Kubernetes Namespace Strategy

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

- `05.07`

Out Of Scope:

- Per-app namespaces.

## 05.09 Generate Deployment Manifest

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
- `05.06`

Out Of Scope:

- StatefulSets.

## 05.10 Generate Service Manifest

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

- `05.09`

Out Of Scope:

- LoadBalancer services.

## 05.11 Apply App Resources

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

- `05.07`
- `05.08`
- `05.09`
- `05.10`

Out Of Scope:

- Ingress.

## 05.12 Wire Deploy API To Kubernetes Apply

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
- `05.11`

Out Of Scope:

- Streaming rollout status.

## 05.13 Add Rollout Status Reader

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

- `05.12`

Out Of Scope:

- Watch-based streaming.

## 05.14 Add Logs Command Via Kubernetes

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

- `05.13`
- `02.04`

Out Of Scope:

- Long-term log storage.
