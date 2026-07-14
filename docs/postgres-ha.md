# PostgreSQL High Availability

Self-Hosted Deployer can reconcile an app-specific PostgreSQL cluster through
CloudNativePG. The default topology is one primary and two streaming standbys,
with each instance placed on a different Kubernetes node.

This feature provides database data redundancy and controlled failover. It is
not generic stateful replication, storage replication, a backup system, or a
complete highly available platform.

## Supported Versions

- CloudNativePG: 1.30.0
- Kubernetes: 1.34, 1.35, or 1.36
- PostgreSQL major version: 14 through 18
- PostgreSQL operand architecture: `linux/amd64` or `linux/arm64`

CloudNativePG 1.30.0 adds a per-cluster Kubernetes Lease that serializes
primary promotion. Primary isolation still provides fencing. The deployer also
enables synchronous replication with `method: any` and
`failoverQuorum: true`.

Install the pinned operator manifest on the Kubernetes control-plane host:

```bash
sudo ./scripts/install-cnpg.sh
```

The installer:

- selects `k3s kubectl` when run as root on a k3s server, otherwise `kubectl`;
- rejects unsupported Kubernetes server versions;
- downloads the official 1.30.0 manifest from immutable upstream commit
  `4b5e244a7d031f67e025c83c1555e7726ecbbfa1`;
- verifies SHA256
  `f8bede43fe4ee0d478c2355b204a36876b2ae4faac60f2a9452280b293da3b88`;
- rewrites both operator image references to the immutable multi-architecture
  digest `sha256:a2701eb97cdd2a34b1fdb2cb51987f544b706e40bec72ae7146cd8580efefebb`;
- applies the manifest with server-side apply;
- refuses to replace an existing `cnpg-controller-manager` that uses a
different operator image; and
- waits for the Cluster and FailoverQuorum CRDs and operator Deployment to
  become ready.

The script is idempotent. Re-running it applies the same pinned manifest.
Use `--client kubectl` or `--client k3s` to override automatic client
selection, and `--timeout 10m` to change the rollout timeout.

The pinned upstream manifest runs one operator replica. PostgreSQL continues
to serve traffic if that pod or its node fails, but reconciliation and
operator-directed failover pause until Kubernetes reschedules the operator.
This is another reason the default operator installation is not a complete
high-availability control plane.

The upstream installation, upgrade, and release-integrity guidance is at
<https://cloudnative-pg.io/docs/1.30/installation_upgrade/>.

## Infrastructure Gates

Do not migrate production data until all of these gates pass:

1. The Kubernetes server is a supported version and the CloudNativePG operator
   is ready.
2. At least three Ready, schedulable nodes have the same architecture as
   `placement.arch`.
3. Those nodes represent independent physical failure domains. Three virtual
   nodes on one host do not protect against host loss.
4. Every database node has enough durable disk capacity. Prefer a dedicated,
   UPS-backed SSD. Raspberry Pi SD cards are not suitable for a production
   PostgreSQL write workload.
5. Pod networking and the Kubernetes API remain usable by a quorum after any
   single node or network path fails.
6. An independent, off-cluster backup exists and has passed a restore drill.
7. The application has a tested write-quiescing procedure and a rollback
   window for the cutover.

The k3s `local-path` storage class creates a local volume on each selected
node. It does not replicate blocks between nodes. PostgreSQL streaming
replication supplies the redundant copies, so loss of a node or disk consumes
one database copy. Replacement and reseeding behavior must be exercised before
production use.

A single k3s server, ingress host, deployer server, or WireGuard hub is still a
platform single point of failure. Three PostgreSQL instances cannot make the
platform available if all surviving nodes lose their Kubernetes API or network
path through the same failed VPS. Full platform fault tolerance needs a
highly available k3s control plane and independent or meshed networking.

## Configuration

Use an image tag that begins with its PostgreSQL version and pin the image with
an immutable lowercase SHA256 digest. CloudNativePG 1.30 supports PostgreSQL
major versions 14 through 18. This is a pinned PostgreSQL 17.10 example:

```yaml
name: my-api
image: ghcr.io/example/my-api:1.0.0

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

secrets:
  - JWT_SECRET

state:
  mode: stateless

resilience:
  mode: resilient

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

`database.postgres` supports:

| Field | Requirement and behavior |
| --- | --- |
| `instances` | 3 through 9; default 3 |
| `image` | Required version-leading tag plus lowercase `@sha256` digest; PostgreSQL major 14 through 18 |
| `database` | Required unquoted PostgreSQL identifier; `postgres`, `template0`, and `template1` are reserved |
| `owner` | Required unquoted PostgreSQL identifier; PostgreSQL and CloudNativePG system roles and the `pg_` or `cnpg_` prefixes are reserved |
| `connectionEnv` | Application environment name; default `DATABASE_URL` |
| `connectionMode` | `external` or `managed`; default `managed` |
| `storage.size` | Required Kubernetes quantity, at least `1Gi` |
| `storage.storageClass` | Default `local-path` |
| `synchronous.replicas` | At least 1 and less than `instances`; default 1 |
| `synchronous.dataDurability` | `required` or `preferred`; default `required` |
| `retentionPolicy` | Only `retain` is supported |

The generated CloudNativePG `Cluster` is `<app>-db`. Required hostname
anti-affinity gives each instance a distinct node, and the database inherits
the architecture from `placement.arch`. Initial cluster creation fails when
too few matching Ready nodes exist. Later app and secret reconciliations remain
available during a node outage because they do not change database placement.
The app name plus the `-db` suffix must fit CloudNativePG's 50-character
Cluster name limit, so an app with a managed database can use at most 47
characters. It must start with a lowercase letter because the generated
Cluster name is a Kubernetes DNS-1035 label.

The deployer enables page checksums when the database is initialized. Normal
app deployments treat the PostgreSQL image, instance count, storage size and
class, bootstrap database and owner, placement architecture, checksum setting,
synchronous replica count, and durability policy as immutable. Database image
changes, storage expansion, scaling, and replication-policy changes need a
dedicated maintenance workflow with compatibility, capacity, backup, and
rollback gates. Only the application `connectionMode` transition is part of
the staged app cutover workflow.

### Managed Connection Security

Managed clusters reject every non-TLS client connection and require the
application database and owner to authenticate with SCRAM-SHA-256 over TLS.
The deployer also owns `password_encryption: scram-sha-256`.

Managed app pods receive these connection policy variables in addition to the
operator-generated URI:

```text
PGSSLMODE=require
PGCHANNELBINDING=require
PGREQUIREAUTH=scram-sha-256
```

The three names are reserved and cannot also appear in the app `secrets` list
or be used as `connectionEnv`. Money Manager's pgx v5.10 client honors this
contract and uses SCRAM channel binding, which prevents a TLS-terminating relay
without depending on CloudNativePG's automatically rotating CA file. A client
that does not support these connection policy variables should use
`connectionMode: external` until its database driver is made compatible. This
mode provides mandatory encrypted and channel-bound authentication, but it is
not PKI hostname verification.

### Durability Choice

With three instances and one synchronous replica, a synchronous commit is
acknowledged only after its WAL reaches the primary and one standby.
Failover quorum prevents promotion when CloudNativePG cannot prove that the
candidate has all acknowledged synchronous commits.

- `required` prioritizes durability. Writes using synchronous commit pause if
  no required standby is healthy.
- `preferred` prioritizes continuity. CloudNativePG can reduce the number of
  required synchronous standbys, so writes may continue with a risk of data
  loss after another failure.

With `required`, loss of the primary alone leaves two replicas and allows safe
failover. Loss of the primary plus one replica leaves only one candidate and
safe failover is refused. Do not force promotion unless accepting possible
data loss is an explicit incident decision.

See the CloudNativePG
[synchronous replication](https://cloudnative-pg.io/docs/1.30/replication/)
and [failover quorum](https://cloudnative-pg.io/docs/1.30/failover/)
documentation for the complete failure semantics.

## Staged Migration

For an existing database, start with `connectionMode: external` and keep the
existing connection name in `secrets`:

```yaml
secrets:
  - DATABASE_URL
  - JWT_SECRET

database:
  postgres:
    # The remaining fields are the same as the managed example.
    connectionEnv: DATABASE_URL
    connectionMode: external
```

External mode creates and reconciles `<app>-db`, but the application continues
to receive `DATABASE_URL` from the deployer's encrypted app secret. It does
not direct application traffic to the new database.

Use this cutover sequence:

1. Confirm the independent backup and restore drill, recovery point, and
   rollback owner.
2. Deploy the external-mode config and wait for all PostgreSQL instances to be
   ready on distinct nodes.
3. Load a non-authoritative copy of the existing database into `<app>-db` and
   validate schema, extensions, row counts, constraints, and application
   queries. Use migration tooling appropriate for the source PostgreSQL
   version and workload.
4. Start the maintenance window and stop all application writes. A dump taken
   while writes continue is not a final cutover snapshot.
5. Take the final backup, restore it into the new cluster, and repeat the data
   reconciliation checks. Record counts or checksums from both sides.
6. Change `connectionMode` to `managed` and remove `connectionEnv` from the
   top-level `secrets` list. Deploy the config. The application environment is
   then sourced from Secret `<app>-db-app`, key `uri`.
7. Verify migrations, API health, background jobs, write and read behavior,
   database status, backups, and logs before ending the maintenance window.
8. Keep the old database intact and unavailable for writes during the agreed
   rollback window. Retarget and test off-cluster backups for the new cluster.

Do not delete the old database, CloudNativePG Cluster, or PVCs as part of the
cutover. If rollback is needed, restore the old external connection secret,
put `connectionEnv` back in `secrets`, set `connectionMode: external`, and
redeploy after reconciling any writes accepted by the new database.

## Status And Verification

The deployer status command reports database health without credentials:

```bash
deployer status my-api
```

Expected healthy output includes:

```text
DATABASE
STATE       healthy
PHASE       Cluster in healthy state
INSTANCES   3/3 ready
PRIMARY     my-api-db-1
NODES       node-a, node-b, node-c
```

During staged migration, status also warns that the application connection is
still external. Treat `missing`, `not_ready`, or `degraded`, fewer ready
instances than desired, a blank primary, or two instances on one node as a
failed migration gate. Healthy status also verifies the effective instance
count, synchronous policy, failover-quorum state, required hostname
anti-affinity, architecture, TLS rejection rule, and SCRAM policy. Manual drift
in any of those fields degrades status with a concrete warning.

Direct Kubernetes checks are also safe because they do not print connection
secrets:

```bash
sudo k3s kubectl -n deployer-apps get clusters.postgresql.cnpg.io my-api-db
sudo k3s kubectl -n deployer-apps get pods -l cnpg.io/cluster=my-api-db -o wide
sudo k3s kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager
```

Use `DEPLOYER_INGRESS_NAMESPACE` instead of the default `deployer-apps` when it
is configured differently.

### Node Maintenance

`deployer nodes drain` deliberately refuses to evict a CloudNativePG instance
pod. It cordons the node, returns a non-success result naming the database pod,
and does not evict other app pods in that drain attempt. This prevents a
reported-success drain from turning a reboot into an abrupt database failure.
Use CloudNativePG's node-maintenance procedure, verify the replacement instance
and failover quorum are healthy on other nodes, then perform host maintenance.
Uncordon the node if the maintenance is cancelled.

## Retention And Backups

`retentionPolicy` is retain-only. Removing `database.postgres` from the app
config or deleting the app leaves the CloudNativePG Cluster and all PVCs in
place. This prevents an application lifecycle operation from becoming a data
destruction operation.

Replication is not backup. All replicas can copy an operator error, accidental
delete, corrupted data, or malicious write. Configure an independent backup
destination, retention policy, monitoring, and periodic restore drills before
calling the database production-ready. CloudNativePG 1.30 recommends its
Barman Cloud Plugin for object-store backup workflows; backup credentials and
policy remain explicit operator configuration outside this deployer template.
