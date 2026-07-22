# Self-Hosted Deployer Specification

## 1. Project Summary

Self-Hosted Deployer is a CLI-first platform for deploying containerized applications across user-owned Linux servers, including Raspberry Pis, mini PCs, and VPS instances.

The platform is designed for unreliable home infrastructure: residential internet, unstable electricity, machines behind NAT, and small ARM-based servers. Its main promise is not generic database magic or enterprise-grade multi-region Kubernetes. Its main promise is:

> Deploy and keep stateless containerized services available across trusted Linux machines, with a stable VPS front door, automated private networking, health-aware routing, and safe handling of unreliable nodes.

The first version should optimize for personal and family-owned infrastructure in the same broad region.

## 2. Core Product Principles

- CLI-first user experience.
- Self-hosted control plane.
- Trusted nodes only, manually approved by the operator.
- Outbound node enrollment from home networks; no router port forwarding required.
- Stable public ingress through a VPS.
- Single k3s cluster for MVP, with a stable VPS control plane and home/relative machines as workers.
- WireGuard hub-and-spoke private network for MVP.
- Automatic resilience for stateless services.
- Conservative, explicit support for stateful services.
- PostgreSQL high availability only through an explicit CloudNativePG template, never by replicating an arbitrary database container.
- ARM64-first workload support.
- Docker Hub/GHCR-compatible image deployment.

## 3. Target Architecture

```text
Operator laptop
  - deployer CLI
        |
        v
VPS
  - public IP
  - deployer control plane API
  - platform database
  - k3s server/control plane
  - ingress/reverse proxy
  - WireGuard hub
  - optional fallback workload node
        |
        v
Private WireGuard network
        |
        +--> Home server A / Raspberry Pi
        |     - deployer agent
        |     - k3s worker
        |     - app workloads
        |
        +--> Home server B / relative server
        |     - deployer agent
        |     - k3s worker
        |     - app workloads
        |
        +--> Home server C / mini PC
              - deployer agent
              - k3s worker
              - app workloads
```

The VPS is the stable public edge. Public traffic enters through the VPS and is routed over the private WireGuard network to healthy application replicas.

```text
Client
  -> api.example.com
  -> VPS reverse proxy / ingress
  -> healthy app pod on home node over WireGuard
  -> VPS
  -> Client
```

Normal application outbound traffic does not need to go through the VPS unless explicitly configured.

## 4. Major Components

### 4.1 CLI

The CLI is the primary interface for operators.

Responsibilities:

- Authenticate with the control plane.
- Initialize platform configuration.
- Add, list, inspect, drain, and remove nodes.
- Create, deploy, scale, rollback, and delete apps.
- Manage secrets.
- Stream logs.
- Show rollout and health status.
- Validate declarative app configs.
- Generate one-time node join commands.

Implementation language: Go.

Rationale:

- Easy static binaries.
- Strong CLI ecosystem.
- Good Kubernetes client libraries.
- Good Linux service and networking support.
- Straightforward cross-compilation.
- Fits the requirement that the CLI is implemented in Golang.

### 4.2 Control Plane

The control plane runs on the VPS.

Responsibilities:

- Store desired state.
- Store node inventory and status.
- Store encrypted app secrets.
- Issue one-time node join tokens.
- Issue per-node identity credentials.
- Coordinate WireGuard peer configuration.
- Coordinate k3s node enrollment.
- Decide placement for workloads.
- Apply Kubernetes manifests or instruct agents/controllers to apply them.
- Coordinate ingress/reverse proxy routing.
- Track app rollout status.
- Track node heartbeats.

The control plane may expose:

- gRPC API for the CLI.
- gRPC API for agents.
- Plain HTTP health/readiness endpoints for local operational checks.

The platform is intentionally not browser-oriented. gRPC with protobuf is the preferred API style because the platform is command-heavy, Go-based, and used by the CLI plus agents rather than a web UI.

Protobuf contract management:

```text
Source .proto files:
  proto/deployer/v1/*.proto

Generated Go code:
  internal/proto/deployer/v1

Tooling:
  buf.yaml
  buf.gen.yaml
  make proto
  make proto-lint
  make proto-check
```

The `.proto` files are the API source of truth. Generated Go files are committed for simple builds but must not be edited manually.

### 4.3 Agent

The agent runs on every managed Linux node, including the VPS if it participates as a workload node.

Responsibilities:

- Register using a one-time join token.
- Receive or generate node identity credentials.
- Configure WireGuard.
- Join the k3s cluster as a worker.
- Report heartbeats, system metadata, and workload status.
- Apply node-local setup tasks.
- Fetch only the desired state and secrets relevant to workloads assigned to that node.
- Stop stale local workloads during reconciliation.

The agent should not have global administrative permissions in the platform.

### 4.4 k3s

k3s is the lightweight Kubernetes distribution used for workload scheduling.

MVP topology:

```text
VPS:
  k3s server/control plane

Home/relative machines:
  k3s agent/worker nodes
```

The Kubernetes API should be reachable by worker nodes over WireGuard.

k3s handles:

- Container lifecycle.
- Pod scheduling.
- Replica management.
- Restart behavior.
- Readiness/liveness probes.
- Services.
- Secrets and ConfigMaps.
- Basic workload spreading.

The deployer platform handles:

- Node onboarding.
- Private networking.
- Desired state UX.
- Cross-node policy.
- Ingress/routing configuration.
- Secrets distribution policy.
- High-level resilience modes.

### 4.5 WireGuard

MVP private networking should use hub-and-spoke WireGuard.

```text
VPS WireGuard IP: 10.8.0.1
Node A:           10.8.0.11
Node B:           10.8.0.12
Node C:           10.8.0.13
```

The VPS has a public IP and listens for WireGuard connections. Home nodes initiate outbound connections to the VPS, avoiding router port forwarding.

Responsibilities of the platform:

- Allocate private VPN IPs.
- Generate or receive peer public keys.
- Update VPS WireGuard peer config.
- Generate node WireGuard config.
- Revoke peers when nodes are removed.
- Rotate credentials in later versions.

Headscale/Tailscale may be considered later, but raw WireGuard is the preferred MVP dependency because it is simple, self-hosted, and under direct platform control.

### 4.6 Reverse Proxy / Ingress

The VPS is the public entry point.

Possible technologies:

- Caddy
- Traefik
- Nginx
- Envoy
- Kubernetes Ingress controller

MVP routing backend decision:

- Use Kubernetes `Ingress` resources with the default k3s Traefik ingress controller.
- This keeps public routing state in the same Kubernetes API surface as Deployments and Services.
- It avoids managing a separate host-level reverse proxy during the MVP while still matching k3s defaults.
- Caddy remains a later option if automatic HTTPS or host-level proxying becomes simpler than Kubernetes-native ingress.

MVP TLS strategy:

- Use cert-manager with a platform-managed ACME `ClusterIssuer` and the default k3s Traefik ingress controller.
- Setting `DEPLOYER_INGRESS_ACME_EMAIL` enables TLS for app routes and supplies the required ACME contact address. `DEPLOYER_INGRESS_TLS_ISSUER` and `DEPLOYER_INGRESS_ACME_SERVER` may override the defaults.
- TLS-enabled app Ingress resources use a per-app certificate secret and bind Traefik's `websecure` entrypoint. Plain HTTP is intentionally not routed for those app routes in the MVP; clients must use HTTPS.
- When no ACME email is configured, routes are created without TLS and reported with `tls_enabled: false`.
- The cluster must have cert-manager installed before enabling automated TLS; route reconciliation reports an apply failure if the cert-manager API is unavailable.
- App reconciliation applies its Namespace, Deployment, and Service before creating or updating its Ingress.

Responsibilities:

- Terminate TLS.
- Route domains to healthy services.
- Stop routing to unhealthy/offline nodes.
- Support multiple apps and domains.
- Optionally expose maintenance/fallback responses.

Example:

```text
api.example.com -> VPS public IP
VPS proxy -> http://10.8.0.12:3000
```

With multiple replicas:

```text
api.example.com
  -> 10.8.0.12:3000
  -> 10.8.0.13:3000
  -> local VPS fallback:3000
```

## 5. CLI User Experience

### 5.1 Platform Login

```bash
deployer login https://deploy.example.com
```

### 5.2 Add Node

```bash
deployer nodes add pi-kitchen --location home-a --arch linux/arm64
```

Output:

```bash
Run this on the new server:

curl -fsSL https://deploy.example.com/install.sh | sudo sh -s -- \
  --token dep_join_abc123
```

The join token should be one-time use and expire quickly.

### 5.3 List Nodes

```bash
deployer nodes list
```

Example output:

```text
NAME          STATUS    ARCH         LOCATION   APPS
pi-kitchen    online    linux/arm64  home-a     3
pi-garage     offline   linux/arm64  home-b     1
vps-edge      online    linux/arm64  vps        2
```

### 5.4 Deploy App From Flags

```bash
deployer apps create my-api \
  --image ivan/my-api:1.0.0 \
  --domain api.example.com \
  --replicas 2 \
  --port 3000
```

### 5.5 Deploy App From Config

Each application can include a `deployer.yaml`.

```yaml
name: my-api
image: ivan/my-api:1.0.0

service:
  port: 3000
  health:
    path: /health

routing:
  domain: api.example.com

deploy:
  replicas: 2
  strategy: rolling

placement:
  arch: linux/arm64
  spread: true
  prefer:
    - location: home
  fallback:
    - location: vps

secrets:
  - JWT_SECRET

database:
  postgres:
    instances: 3
    image: ghcr.io/cloudnative-pg/postgresql:17.10-202606221003-system-bookworm@sha256:f7ee6ba4f221a4c8ee8a83edf6e7eb8acd373fb118237665080ab6f9cdec8618
    database: my_api
    owner: my_api
    connectionEnv: DATABASE_URL
    connectionMode: managed
    storage:
      size: 20Gi
      storageClass: local-path
    synchronous:
      replicas: 1
      dataDurability: required
    retentionPolicy: retain
```

Deploy:

```bash
deployer deploy
```

By default, `deployer deploy` reads `./deployer.yaml`. Operators can pass an explicit relative or absolute path:

```bash
deployer deploy --file ./deploy/deployer.yaml
deployer deploy -f /opt/apps/my-api/deployer.yaml
deployer deploy --file ./deploy/deployer.yaml --dry-run
```

Expected behavior:

1. CLI reads `deployer.yaml`.
2. CLI validates required fields.
3. CLI sends desired state to the control plane.
4. Control plane updates Kubernetes manifests/routing state.
5. CLI streams rollout status until completion or failure.

### 5.6 Secrets

```bash
deployer secrets set my-api DATABASE_URL
```

The CLI should prompt securely:

```text
DATABASE_URL: [input hidden]
```

For automation, a value may be passed explicitly, but this is less safe because
the value may remain in shell history:

```bash
deployer secrets set --value 'postgres://...' my-api DATABASE_URL
```

List secret names:

```bash
deployer secrets list my-api
```

Remove a secret after confirmation, or use `--yes` for automation:

```bash
deployer secrets remove my-api DATABASE_URL
deployer secrets remove --yes my-api DATABASE_URL
```

Secret values are never returned by the secret API or printed by the CLI.

### 5.7 Status

```bash
deployer status my-api
```

Example:

```text
APP      IMAGE              HEALTHY  DESIRED  ROUTE
my-api   ivan/my-api:1.0.0  2        2        api.example.com

REPLICAS
pi-kitchen   healthy   10.8.0.12:3000
vps-edge     healthy   10.8.0.1:3000
```

### 5.8 Logs

```bash
deployer logs my-api --follow
```

### 5.9 Scaling

```bash
deployer scale my-api --replicas 3
```

### 5.10 Drain Node

```bash
deployer nodes drain pi-garage
```

Drain means:

- Do not schedule new workloads on the node.
- Move stateless workloads away when possible.
- Keep the node registered.

### 5.11 Remove Node

```bash
deployer nodes remove pi-garage
```

Remove means:

- Revoke node identity.
- Remove WireGuard peer.
- Remove or mark Kubernetes node unavailable.
- Stop scheduling workloads to it.

## 6. Desired State And Reconciliation

The platform should follow a GitOps-style reconciliation model.

This means it stores desired state and continuously makes actual state match it.

Desired state example:

```yaml
app: my-api
image: ghcr.io/me/my-api:v1.4.2
replicas: 2
domain: api.example.com
placement:
  arch: linux/arm64
  spread: true
```

Actual state example:

```text
pi-kitchen: my-api v1.4.1 running
pi-garage: offline
vps-edge: no fallback running
```

Reconciliation actions:

- Update `my-api` to `v1.4.2`.
- Start replacement replica where capacity exists.
- Remove offline endpoints from routing.
- Update Kubernetes objects.
- Report rollout status.

The user should not need to SSH into machines for normal deployment operations.

## 7. Resilience Model

### 7.1 Resilience Goal

The platform should make stateless services resilient to:

- home server power loss
- temporary internet loss
- node disappearance
- container crashes
- app health check failures

It should do this by:

- running multiple replicas
- spreading replicas across nodes
- routing only to healthy replicas
- rescheduling workloads when nodes remain offline
- optionally running fallback replicas on the VPS

### 7.2 Resilience Modes

Applications should support clear modes instead of exposing raw Kubernetes concepts directly.

```text
Basic:
  one replica
  restart on crash

Resilient:
  two or more replicas
  spread across nodes
  health-checked routing

Fallback:
  prefer home nodes
  start or keep fallback replica on VPS

Pinned:
  run only on selected node
  useful for stateful apps or hardware-bound apps
```

### 7.3 Kubernetes Primitives Used

The platform can map resilience modes to Kubernetes primitives:

- Deployments
- ReplicaSets
- readiness probes
- liveness probes
- Services
- Ingress
- topology spread constraints
- node labels
- node selectors
- taints/tolerations
- PodDisruptionBudgets

Resilient stateless deployments keep every desired replica available during a
rollout, add one temporary surge replica, and only advance after the new
revision remains ready for a short stability window. Revision-aware topology
spreading lets the surge replica share a node temporarily without allowing the
completed revision to collapse onto one node.

Public routes attach a Traefik retry middleware and a bounded backend dial
timeout. This lets idempotent requests move to another healthy replica while
Kubernetes is still removing a stale endpoint after a node network failure.

Example topology spread:

```yaml
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: DoNotSchedule
    matchLabelKeys:
      - pod-template-hash
    labelSelector:
      matchLabels:
        app: my-api
```

## 8. Secrets Management

### 8.1 Auth Versus Secret Storage

Admin authentication and application secret storage are separate concerns.

An identity provider can secure operator login later, but application secrets should be stored in the platform's secret repository, not in the login system.

Application secrets include:

- `DATABASE_URL`
- `JWT_SECRET`
- `STRIPE_SECRET_KEY`
- `S3_SECRET_ACCESS_KEY`

### 8.2 MVP Secret Storage

For MVP, secrets can be stored encrypted in the platform database.

Requirements:

- Secret values encrypted at rest.
- Encryption key stored outside the database.
- Secret values never returned by the API or printed by the CLI.
- Secrets distributed only to nodes/clusters running the relevant app.
- Secrets scoped per app.
- Kubernetes Secrets created only where needed.

The control plane requires exactly one encryption key source at startup:

```text
DEPLOYER_SECRET_KEY       -> raw 32-byte AES-256 key
DEPLOYER_SECRET_KEY_FILE  -> path to a file containing the raw 32-byte key
```

Key files are read byte-for-byte and must contain exactly 32 bytes; a trailing
newline counts as key material. Secret values are encrypted before database
storage with AES-256-GCM and a fresh random nonce for every update.

When an app configuration declares secret names, the control plane creates one
Kubernetes Secret for that app containing only those names and injects them as
environment variables in the Deployment. A required name that has not been
set blocks deployment, while secret values may be set one at a time before the
complete set is deployed. Updating a referenced value changes a pod-template
hash derived from encrypted secret state and starts a rollout, without exposing
a plaintext-value hash. Removing a currently referenced secret is rejected
until it is removed from the app configuration.

More advanced future approach:

```text
master key
  -> encrypts per-secret data keys
  -> data keys encrypt individual secret values
```

### 8.3 Future Secret Options

Future integrations may include:

- Kubernetes encryption at rest
- SOPS
- Sealed Secrets
- External Secrets Operator
- HashiCorp Vault
- cloud KMS providers

## 9. Persistent Storage

### 9.1 Core Constraint

Stateless apps are easy to move and replicate.

Stateful apps are not automatically safe to move across unreliable home networks.

Kubernetes can replicate processes. It cannot automatically make arbitrary application data safely replicated.

### 9.2 MVP State Modes

Apps should declare a state mode.

```text
Stateless:
  no durable local state
  can run many replicas anywhere

Stateful pinned:
  has a local volume
  runs only on selected node

Stateful with backup:
  pinned primary storage
  periodic backups to VPS/object storage

External database:
  app stores state outside the platform

Replica-aware:
  app/database manages its own replication
```

### 9.3 Recommended MVP Support

MVP should support:

- stateless deployments with automatic resilience
- pinned stateful deployments
- backup hooks or scheduled backup commands

MVP should not claim generic automatic failover for arbitrary databases.

### 9.4 PostgreSQL High Availability Template

PostgreSQL is the first supported database-specific replication template. It
uses CloudNativePG 1.30.0 on Kubernetes 1.34 through 1.36. The operator must be
installed before an app containing `database.postgres` is deployed.

The template has the following contract:

- `instances` is between 3 and 9 and defaults to 3.
- `image` is required, its tag must begin with a PostgreSQL version whose major
  is between 14 and 18, and it must end with an immutable lowercase `@sha256`
  digest.
- `database` and `owner` are required PostgreSQL identifiers. System databases
  and PostgreSQL or CloudNativePG system roles are rejected so the application
  never receives superuser or operator credentials.
- `connectionEnv` defaults to `DATABASE_URL`.
- `connectionMode` is either `external` or `managed` and defaults to `managed`.
- `storage.size` is required and must be at least `1Gi`.
- `storage.storageClass` defaults to `local-path`.
- `synchronous.replicas` defaults to 1 and must be less than `instances`.
- `synchronous.dataDurability` is `required` or `preferred` and defaults to `required`.
- `retentionPolicy` only supports `retain`.

The generated CloudNativePG `Cluster` is named `<app>-db`. It inherits
`placement.arch` and supports `linux/amd64` and `linux/arm64`. Reconciliation
requires enough Ready, schedulable nodes of that architecture before creating
the cluster. Existing clusters can still be reconciled during a node outage
because ordinary app and secret updates do not change database placement.
Required pod anti-affinity on
`kubernetes.io/hostname` places every database instance on a different node.
CloudNativePG limits Cluster names to 50 characters, so the app name can use at
most 47 characters after reserving the `-db` suffix, and it must start with a
lowercase letter.

New clusters enable PostgreSQL data checksums. The deployer owns
`password_encryption: scram-sha-256`, rejects all non-TLS client connections,
and installs an application-specific `hostssl` rule requiring SCRAM-SHA-256.
Managed app pods also receive `PGSSLMODE=require`,
`PGCHANNELBINDING=require`, and `PGREQUIREAUTH=scram-sha-256`. Those names are
reserved in managed mode. Normal app deployments treat the PostgreSQL image,
instance count, storage size and class, bootstrap identity, placement,
checksums, synchronous replica count, and durability policy as immutable.
Image changes, storage expansion, scaling, and replication-policy changes
require a dedicated database-maintenance workflow and backup gate. The staged
application cutover can change only `connectionMode` between `external` and
`managed`.

The generated synchronous replication policy uses quorum selection with
`method: any` and enables failover quorum. With the common three-instance,
one-synchronous-replica configuration, an acknowledged synchronous commit has
reached the primary and at least one standby. If only one replica can be
reached after the primary is lost, failover is refused because safe promotion
cannot be proven.

`dataDurability: required` stops synchronous writes when too few healthy
standbys remain. `preferred` reduces the synchronous requirement when
standbys are unavailable and can keep writes available at the cost of possible
data loss. The operator must choose this trade-off explicitly.

`connectionMode: external` still provisions and reconciles `<app>-db`, but the
application keeps using `connectionEnv` from the app's ordinary encrypted
secret. This supports a staged migration from an existing database. In
`managed` mode, `connectionEnv` is injected from the CloudNativePG-generated
Secret `<app>-db-app`, key `uri`, and the same name must not also be declared in
`secrets`.

Removing `database.postgres` from desired state or deleting the app does not
delete the CloudNativePG `Cluster` or its PVCs. Database destruction requires
a separate, explicit operator action outside the app deletion path. App status
reports the database state, CloudNativePG phase, ready and desired instances,
primary, and running nodes without returning credentials. A healthy state also
requires the effective synchronous-replication, failover-quorum, placement,
TLS, and SCRAM policies to match the desired template.

This template is not a backup system. It requires independent backups and a
tested restore procedure. It also does not make the full platform highly
available by itself. Full platform fault tolerance requires at least three
same-architecture failure domains, a highly available Kubernetes control
plane, and networking that continues to connect a quorum when any single node
or network hub fails. The k3s `local-path` storage class does not replicate
storage, and removable flash media is not recommended for production database
volumes.

### 9.5 Future Stateful Templates

Possible future templates:

- Redis cache
- CouchDB replicated nodes
- MinIO object storage
- Litestream-style SQLite backup

Distributed databases can be supported as app-specific templates, not as a universal platform guarantee.

## 10. Image Distribution

### 10.1 MVP

Use public or private container registries:

- Docker Hub
- GitHub Container Registry
- other OCI-compatible registries

Apps specify images:

```yaml
image: ivan/my-api:1.0.0
```

### 10.2 Registry Credentials

Support registry credentials for private images:

```bash
deployer registry login docker.io
```

Registry credentials should be treated as secrets.

### 10.3 Future Improvements

- VPS registry mirror.
- Image pre-pull on nodes.
- Local image cache.
- Multi-architecture manifest validation.
- Private registry hosted by the platform.

## 11. Node Trust And Identity

Manual approval is required, but cryptographic identity is still necessary.

### 11.1 Join Flow

```text
1. Admin runs `deployer nodes add`.
2. Control plane creates one-time join token.
3. Admin runs install command on the new node.
4. Agent exchanges join token for node identity credentials.
5. Join token expires or is consumed.
6. Node uses its own certificate/key for future communication.
```

MVP token behavior:

```text
Admin token:
  generated during server bootstrap
  displayed once
  pasted into CLI login
  stored by server as hash

Join token:
  generated by server when CLI adds a node
  displayed once in the join/install command
  one-time use
  short-lived
  stored by server as hash

Agent token:
  generated by server during node join
  returned directly to the agent
  stored locally by the agent
  not normally displayed in CLI
  stored by server as hash
```

Tokens should be generated with cryptographically secure randomness, have at least 256 bits of entropy, use readable type prefixes such as `dep_admin_`, `dep_join_`, and `dep_agent_`, and never be logged in plaintext.

Token storage and validation:

```text
Raw token:
  displayed or returned once
  used by CLI or agent
  never stored by the control plane

Token hash:
  stored in the control plane database
  used for future validation

Token lifecycle:
  database-backed so it survives server restarts
```

MVP token tables:

```text
admin_tokens:
  id
  name
  token_hash
  created_at
  last_used_at
  revoked_at

node_join_tokens:
  id
  node_name
  token_hash
  labels_json
  expires_at
  used_at
  created_at

agent_tokens:
  id
  node_id
  token_hash
  created_at
  last_used_at
  revoked_at
```

On every authenticated gRPC request, the control plane reads the authorization metadata, hashes the provided token with the server token hashing key, checks the relevant database table, rejects expired/used/revoked tokens, and attaches the caller identity to the request context.

### 11.2 Node Permissions

A node should be allowed to:

- report its own health
- report its own workload status
- fetch workloads assigned to it
- fetch secrets for workloads assigned to it

A node should not be allowed to:

- fetch all platform secrets
- impersonate another node
- modify global desired state
- enroll unlimited new nodes

### 11.3 Revocation

Removing a node should:

- revoke its platform credentials
- remove its WireGuard peer
- mark or remove Kubernetes node access
- remove it from routing

## 12. Offline Behavior

Nodes should have explicit lifecycle states.

```text
Pending:
  join token created but node has not enrolled

Online:
  node is connected and healthy

Degraded:
  node is connected but some workloads or checks are unhealthy

Offline:
  node missed heartbeats for a configured threshold

Drained:
  node is trusted but should not receive new workloads

Quarantined:
  node returned after a long absence and requires reconciliation or approval

Removed:
  node no longer belongs to the platform
```

When a node goes offline:

1. Mark it offline.
2. Remove its endpoints from routing.
3. Reschedule stateless workloads if capacity exists.
4. Keep its identity unless explicitly removed.

When a node returns:

1. Authenticate using node identity.
2. Report actual local state.
3. Reconcile against desired state.
4. Stop stale workloads that should no longer run.
5. Start assigned workloads.
6. Rejoin routing only after health checks pass.

Offline does not mean removed.

## 13. Multi-Architecture Support

### 13.1 MVP Architecture

MVP supports workload scheduling for:

```text
linux/arm64
```

This matches:

- Raspberry Pi 4/5 running 64-bit Linux
- many ARM Linux servers
- Linux ARM64 containers built from Apple Silicon Macs

Apple Silicon Macs are `darwin/arm64`, but containers for Raspberry Pi must target `linux/arm64`.

Build example:

```bash
docker buildx build --platform linux/arm64 -t ivan/my-api:1.0.0 .
```

The same distinction applies to the platform binaries. Apple Silicon Macs and Raspberry Pis are both ARM64, but they do not run the same binary because the operating system target is different:

```text
Apple Silicon Mac:
  darwin/arm64

Raspberry Pi with 64-bit Linux:
  linux/arm64
```

Required MVP binary targets:

```text
deployer_darwin_arm64
  CLI for Apple Silicon Macs

deployer_linux_arm64
  CLI for ARM64 Linux machines

deployer-server_linux_arm64
  control plane for ARM64 Linux VPS/server

deployer-agent_linux_arm64
  agent for Raspberry Pi / ARM64 Linux nodes
```

Recommended early extra target:

```text
deployer-server_linux_amd64
  control plane for common AMD64 VPS providers
```

### 13.2 VPS Consideration

If fallback workloads should run on the VPS, the VPS should either:

- be ARM64, or
- only run images that also support AMD64.

The control plane may run on AMD64 even if workloads are ARM64-only, but fallback workload scheduling must respect architecture.

### 13.3 Future Multi-Arch Support

Future platform versions should support:

- `linux/arm64`
- `linux/amd64`
- multi-arch OCI manifests
- scheduling based on node architecture
- pre-deploy image compatibility validation

## 14. App Replication Semantics

### 14.1 Important Distinction

Kubernetes can replicate application processes.

Kubernetes cannot automatically make arbitrary application data safely replicated.

Easy:

```text
Run 3 interchangeable API containers.
```

Hard:

```text
Run 3 database containers and keep writes correct during network partitions.
```

### 14.2 App State Classes

The platform should classify apps:

```text
Stateless service:
  safe to replicate and move

Stateful pinned service:
  durable local state, fixed node

Cache:
  data can be lost and rebuilt

Worker:
  can restart, but jobs should be idempotent

Replica-aware database:
  app/database handles its own replication
```

### 14.3 MVP Policy

Only stateless services get generic automatic resilience and failover.

Stateful services require explicit configuration:

- pinned node
- backup policy
- external database
- app-specific replication template

The CloudNativePG PostgreSQL template is the first supported app-specific
replication template. The platform must not pretend to provide safe automatic
failover for arbitrary stateful apps.

## 15. Kubernetes Cluster Strategy

### 15.1 MVP: One Cluster Across Trusted Regional Networks

Given that all machines are trusted and geographically close, the MVP can use one k3s cluster across private networks.

Recommended:

```text
VPS:
  k3s server/control-plane

Home/relative servers:
  k3s worker nodes
```

This provides:

- one Kubernetes API
- one scheduler
- one deployment model
- simple horizontal scaling
- centralized ingress

### 15.2 Constraints

The control plane should live on the most reliable node, preferably the VPS.

Avoid placing the main Kubernetes control plane on a flaky home server.

Workers can be unreliable. The control plane should remain stable.

Database replicas on worker nodes do not remove control-plane or network
single points of failure. Full platform fault tolerance requires an odd-sized,
highly available k3s server topology and network paths that do not depend on a
single WireGuard hub. A single VPS running k3s, ingress, and the WireGuard hub
remains a platform-wide failure domain even when PostgreSQL has three healthy
instances.

### 15.3 Future: Multi-Cluster

Later versions may support multiple k3s clusters:

```text
Cluster A: home/site 1
Cluster B: home/site 2
Cluster C: VPS fallback
```

The platform would then manage resilience across clusters rather than inside one cluster.

This is more complex and should not be the MVP unless one-cluster networking becomes too fragile.

## 16. Security Model

### 16.1 Assumptions

- Nodes are owned/trusted by the operator or close relatives.
- Node enrollment requires operator approval.
- Home networks are not directly exposed to the internet.
- The VPS is trusted and acts as the public edge.

### 16.2 Requirements

- All CLI/API communication must use TLS.
- Agent/control-plane communication must authenticate nodes.
- Join tokens must be one-time and short-lived.
- Secrets must be encrypted at rest.
- Nodes should receive only secrets they need.
- Removed nodes must lose access.
- Public ingress should support TLS certificates.
- Control plane admin access should require authentication.

### 16.3 Later Enhancements

- OIDC admin auth.
- mTLS between agents and control plane.
- Per-app service accounts.
- Audit log.
- Secret rotation.
- Node quarantine rules.

## 17. Data Model Draft

### 17.1 Node

```text
id
name
status
location
arch
wireguard_ip
wireguard_public_key
last_seen_at
labels
taints
created_at
removed_at
```

### 17.2 App

```text
id
name
image
state_mode
replicas
port
health_path
domain
resilience_mode
placement_policy
created_at
updated_at
```

### 17.3 Secret

```text
id
app_id
name
encrypted_value
created_at
updated_at
```

### 17.4 Deployment

```text
id
app_id
image
desired_replicas
status
started_at
completed_at
failed_reason
```

### 17.5 Route

```text
id
app_id
domain
tls_enabled
target_service
status
```

### 17.6 Node Join Token

```text
id
token_hash
node_name
expires_at
used_at
created_at
```

### 17.7 Admin Token

```text
id
name
token_hash
created_at
last_used_at
revoked_at
```

### 17.8 Agent Token

```text
id
node_id
token_hash
created_at
last_used_at
revoked_at
```

### 17.9 Event

```text
id
created_at
type
severity
message
app_id
node_id
deployment_id
metadata_json
```

Events are domain-level platform records, not raw process logs. They should be emitted on meaningful state transitions and operator actions.

Example event types:

```text
node.joined
node.online
node.offline
node.removed
app.deploy.started
app.deploy.succeeded
app.deploy.failed
app.health.degraded
app.health.recovered
route.created
route.degraded
route.recovered
secret.created
secret.updated
secret.deleted
```

Events should be available through CLI commands:

```bash
deployer events
deployer events --app my-api
deployer events --node pi-garage
deployer events --type app.deploy.failed
deployer events --severity error
deployer events --since 1h
deployer events --watch
```

Event history is retained with conservative server defaults and can be tuned through:

```text
DEPLOYER_EVENT_RETENTION_MAX_AGE    -> maximum event age, default 720h
DEPLOYER_EVENT_RETENTION_MAX_COUNT  -> maximum retained rows, default 10000
DEPLOYER_EVENT_CLEANUP_INTERVAL     -> cleanup frequency, default 1h
```

## 18. MVP Scope

### 18.1 MVP Must Have

- CLI binary.
- Control plane API.
- SQLite or Postgres-backed platform state.
- One-time node join tokens.
- Agent installer command.
- WireGuard hub-and-spoke setup.
- k3s control-plane setup on VPS.
- k3s worker join on nodes.
- ARM64 workload support.
- Deploy stateless app from image.
- `deployer.yaml` support.
- App replicas.
- Health checks.
- Basic ingress/routing through VPS.
- Encrypted app secrets.
- Node status and heartbeats.
- App status.
- Logs command.
- App-specific CloudNativePG PostgreSQL high availability template.

### 18.2 MVP Nice To Have

- Caddy/Traefik automatic TLS.
- Node drain.
- Rollback.
- Registry credentials.
- Basic backup hooks for pinned stateful apps.
- `deployer doctor` diagnostics.
- Platform event log.
- Local developer install target for the CLI, e.g. `make install-cli` into `$HOME/.local/bin`.

### 18.3 Out Of Scope For MVP

- Generic stateful app failover.
- Multi-cluster orchestration.
- Full admin web dashboard.
- Multi-architecture scheduling.
- Hosted SaaS control plane.
- Automatic database partitioning.
- General-purpose distributed storage.
- Complex RBAC for many human users.
- GitHub Releases based automated platform updates.

## 19. Suggested Implementation Phases

### Phase 1: Local Skeleton

- Create CLI project.
- Create control plane API.
- Add database schema.
- Implement app config parsing.
- Implement basic desired state storage.
- Add `deployer apps create`, `deployer deploy`, `deployer apps list`.

### Phase 2: Node Enrollment

- Add node model.
- Add join token generation.
- Add agent binary.
- Add agent registration.
- Add heartbeats.
- Add `deployer nodes add/list/inspect`.

### Phase 3: WireGuard Automation

- Configure VPS as WireGuard hub.
- Allocate VPN IPs.
- Generate node WireGuard config.
- Add peer to VPS.
- Validate node connectivity.

### Phase 4: k3s Integration

- Bootstrap k3s server on VPS.
- Join worker nodes over WireGuard.
- Label nodes with arch/location.
- Apply Kubernetes Deployment/Service manifests.
- Read Kubernetes rollout status.

### Phase 5: Ingress And Routing

- Install/configure Caddy or Traefik.
- Map app domains to services.
- Health-aware routing.
- Remove offline endpoints.
- Add TLS support.

### Phase 6: Secrets

- Add encrypted secret storage.
- Add CLI secret commands.
- Create Kubernetes Secrets only for target apps.
- Ensure values are not printed in logs/status output.

### Phase 7: Resilience UX

- Add resilience modes.
- Add topology spread constraints.
- Add fallback scheduling.
- Add offline node behavior.
- Add drain/remove flows.

### Future Phase: Releases And Automated Updates

- Build CLI, server, and agent binaries with GitHub Actions.
- Upload versioned artifacts to GitHub Releases.
- Publish checksums and eventually signatures.
- Add version-check commands.
- Add a controlled way to update the VPS control plane.
- Add a controlled way for the VPS control plane to roll out agent updates to worker nodes.
- Support safe rollback to the previous server or agent version.

## 20. Open Design Questions

- Should the control plane apply Kubernetes manifests directly, or should agents perform local reconciliation?
- Should the MVP use SQLite on the VPS or Postgres from the start?
- Should the reverse proxy be managed outside Kubernetes or as Kubernetes ingress?
- Should WireGuard be configured by the agent directly or by generated config files plus systemd?
- Should fallback VPS workloads be always running or started only during outage?
- What is the minimum supported Raspberry Pi model?
- Should the platform install Docker/containerd itself, or assume k3s manages containerd?
- Should app logs be read from Kubernetes directly by the control plane, or streamed through agents?
- Should node return after long absence require automatic reconciliation or manual approval?

## 21. Working Definition Of Success

The MVP is successful when the operator can:

1. Provision a VPS as the platform/control-plane node.
2. Add two ARM64 Linux home servers using one-line install commands.
3. Deploy a stateless REST API with two replicas.
4. Expose it at `api.example.com` through the VPS.
5. Pull the power or internet from one home server.
6. See the node marked offline.
7. See traffic continue to reach a healthy replica.
8. Bring the node back online.
9. See it reconcile without manual SSH intervention.
