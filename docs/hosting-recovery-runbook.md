# Separate-cluster managed hosting recovery runbook

Status: qualification runbook for the managed hosting POC, 2026-09-06. This
document describes the evidence required before onboarding a customer and the
operator sequence for rehearsal, recovery, and offboarding. It does not claim
that the POC currently has full-environment backup or restore coverage.

The POC boundary is one customer per dedicated VM or cluster. Each environment
has its own deployer server, SQLite state, credentials, WireGuard network,
k3s installation, Kubernetes workloads, and backup prefix. Customer
environments do not share the existing three-node cluster or its ingress.
Follow [the POC plan](hosting-poc-plan.md) for the broader qualification
checklist and [the implementation status](hosting-implementation-status.md)
for current evidence and open work.

## Recovery boundary and evidence status

The only implemented backup primitive is the authenticated encrypted snapshot
of the deployer server's SQLite database. It uses a read-only SQLite connection
and `VACUUM INTO`, refuses to overwrite an existing destination, and verifies
the decrypted payload with SQLite integrity checking. The exact operator
behavior is in [Platform SQLite backup](platform-backup.md).

That artifact is not a complete environment backup. It does not stop writers,
coordinate Kubernetes, recover a k3s datastore, restore configuration or
credentials, restore WireGuard, recover PVC contents, restore application
files, or provide PostgreSQL point-in-time recovery. The database can contain
encrypted application secret values, but decrypting them still requires the
original 32-byte `DEPLOYER_SECRET_KEY` or the file referenced by
`DEPLOYER_SECRET_KEY_FILE`. A backup key for the SQLite artifact is a separate
key and does not decrypt application secrets.

Do not label an environment recoverable until the evidence gates below are
passed for every data class used by that customer:

| Data class | Source or runtime path | Required recovery evidence | Current POC status |
| --- | --- | --- | --- |
| Platform desired state and events | `DEPLOYER_DATABASE_URL`, normally `file:/var/lib/deployer/deployer.db` | Verified encrypted snapshot, key retrieval, integrity check, compatible binary, and reconciliation in an isolated environment | SQLite create and restore primitive implemented; full rehearsal open |
| Server configuration and service identity | `/etc/deployer/server.env`, `/etc/systemd/system/deployer-server.service`, installed binary and update policy, normally `/etc/deployer/update-policy.conf` | Sanitized manifest plus protected copy of exact values and release checksums; restore starts without exposing secrets | Not qualified off host |
| Application secret decryption | `DEPLOYER_SECRET_KEY` or `DEPLOYER_SECRET_KEY_FILE`; token hashing uses `DEPLOYER_TOKEN_HASH_KEY` | Key custody test and secret read test after restore; never put key material in the database backup or customer export | Key custody is unresolved |
| TLS and public endpoint | `DEPLOYER_SERVER_TLS_CERT_FILE`, `DEPLOYER_SERVER_TLS_KEY_FILE`, `DEPLOYER_PUBLIC_BASE_URL` | Certificate and private key restored through the approved secret store, endpoint ownership and HTTPS check pass | Not qualified |
| k3s control plane | `/etc/rancher/k3s/config.yaml`, `/etc/rancher/k3s/k3s.yaml`, k3s service state, and the installed k3s datastore for the actual topology | Topology-specific clean rebuild or datastore restore rehearsal, followed by API, namespace, object, and workload checks | Not qualified; do not assume an etcd snapshot applies to a SQLite k3s server |
| k3s server identity | `/var/lib/rancher/k3s/server/token`, any `node-token` reference, and topology-specific server credentials | Clean environment can join or recover the intended nodes without rotating identities unexpectedly | Not qualified |
| WireGuard hub and node identity | Server `DEPLOYER_WIREGUARD_INTERFACE` and related settings; agent `/etc/deployer/wireguard/privatekey`, `/etc/wireguard/wg0.conf` | Peer and route connectivity test with expected public keys and no cross-customer reachability | Not qualified |
| Agent identity and runtime | Agent `/etc/deployer/agent.env`, `/etc/deployer/agent/token`, `/etc/rancher/k3s/config.yaml`, and `deployer-agent.service` | Agent re-enroll or restore test, heartbeat, k3s worker health, and credential revocation check | Not qualified |
| Kubernetes application state | Namespaces, Deployments, Services, Ingresses, Secrets, ConfigMaps, PVCs, and retained database objects | Object inventory and workload restore test with representative application checks | Not implemented as a general backup |
| Application data | App-specific files, object storage, external database, or PVC contents | Application owner-approved backup and restore test with measured RPO and RTO | Required before onboarding any stateful workload |
| Managed PostgreSQL data | CloudNativePG cluster and its retained PVCs, if ever offered | Pinned backup integration, WAL handling, point-in-time restore, and application traffic test | Excluded from the POC offering until separately qualified |
| Monitoring and audit evidence | Monitoring namespace resources and off-host mutation records | Alert delivery and audit retention survive loss of the customer cluster | Off-host forwarding and delivered alerts remain open |

The [official k3s recovery documentation](https://docs.k3s.io/datastore/backup-restore) requires the original server token alongside the datastore because it protects confidential datastore contents. For the default SQLite topology, the datastore directory is `/var/lib/rancher/k3s/server/db/`. Preserve the token and datastore as a consistent recovery set using the rehearsed procedure; this deployer database command does not back up either. Custom k3s data directories must be recorded explicitly.

The table is a qualification record. Replace each unresolved status with an
artifact location, rehearsal date, measured result, and operator who approved
it. A healthy process endpoint or a successful SQLite integrity check is not
evidence for the other rows.

## Onboarding qualification

Complete this sequence for a disposable customer environment before accepting
customer data. Use a named CLI context and separate credentials as described in
[CLI contexts](cli-contexts.md). Record the customer environment ID, cluster
identity, release versions, network ranges, backup prefix, and operator.

### 1. Establish the customer boundary

Record the dedicated VM or cluster owner, region, operating system and
architecture, k3s version and datastore mode, node names and roles, WireGuard
CIDR, ingress hostname, deployer endpoint, and Kubernetes contexts. Confirm
that the environment does not use the legacy three-node cluster, its ingress,
or another customer's backup prefix.

Capture read-only baseline evidence for nodes, k3s API health, namespaces,
routes, Pods, PVCs, resource pressure, certificate expiry, and the deployer
version. Do not copy secret values, private keys, live database files, or
tokens into the qualification record. [Hosting inventory](hosting-inventory.md)
defines the safe inventory boundary.

### 2. Freeze the recovery inputs

Create a protected inventory of the files and values needed to rebuild the
environment. At minimum include the server environment file path, deployer
binary checksum, systemd unit, update policy, k3s config and kubeconfig paths,
WireGuard interface and configuration paths, agent environment and token
paths, TLS certificate references, and the secret-encryption and token-hash
key custody references. Store references and checksums in the runbook record;
store secret material only in the approved recovery system.

Choose the backup destination, per-environment prefix, retention period,
encryption key owner, recovery access procedure, and access review
cadence before onboarding. These are POC decisions still required by the
[implementation status](hosting-implementation-status.md). The SQLite backup
command does not upload artifacts or manage retention. The opt-in [delivery job](platform-backup-job.md) can upload to an existing restic repository and verify the exact artifact by reading it back. It has passed a disposable local-repository test, but no offsite destination or schedule is configured.

### 3. Qualify application coverage

For each application, record whether it is stateless, uses PVCs, writes to an
external database, or requests managed PostgreSQL. Record the data owner,
backup source, restore procedure, RPO, RTO, retention, and whether deleting
the application leaves the backup and database volume retained.

Do not onboard local persistent data until a representative restore has been
performed. Do not offer managed PostgreSQL based only on synchronous replicas
or retained PVCs. [PostgreSQL HA](postgres-ha.md) describes the distinction
between availability and independent backup evidence.

### 4. Run the synthetic rehearsal

Use generated credentials and representative non-customer data in a clean,
isolated environment. Disable outbound effects until validation is complete.
Exercise a deployment, secret read, route check, application write and read,
backup creation, simulated loss of the original environment, rebuild or
restore, and application validation. Include a backup failure and an old
backup-age alert. Capture elapsed time, manual actions, errors, and the exact
backup and key versions used.

The database-only drill is available as `go test -tags=integration -race ./internal/backup -run TestPlatformRecoveryWithoutOriginalHost -v`. It checks synthetic platform state and key recovery without starting any service. It does not replace the full-environment sequence above.

The rehearsal passes only when an operator without access to the failed host
can recover the stated platform, application, and data scope. Record measured
RPO and RTO against the POC targets of at most 24 hours for platform state and
four hours for a small full-environment recovery, unless a different target is
approved in the customer record. These are qualification targets, not customer
guarantees.

## Recovery procedure

Declare the incident and freeze mutations for the affected environment. Note
the time of the recovery boundary, last known healthy application check, last
successful backup, suspected failure scope, and the operator responsible.

Preserve the failed host and its storage for evidence. Do not overwrite the
live database, delete PVCs, rotate keys, or run a cleanup or reinstall command
while the scope is uncertain. The existing restore primitive intentionally
requires a new destination and a stopped writer; follow
[Platform SQLite backup](platform-backup.md) and the rehearsed procedure for
the exact binary version. Do not improvise a restore over a live database.

Recover in dependency order, recording a pass or failure at each gate:

1. **Recovery authority.** Confirm the customer, environment ID, approved
   recovery boundary, backup artifact checksum, backup encryption key, and
   secret-encryption key are available through their separate custody paths.
   If either key is missing or unverified, stop and declare the corresponding
   data class unrecoverable until the owner resolves it.
2. **Control-plane host.** Build or select a clean host matching the recorded
   architecture and supported versions. Restore the protected server
   configuration and service files only through the approved secret handling
   process. Verify file ownership, permissions, binary checksum, and update
   policy before starting the service.
3. **Platform database.** With all deployer writers stopped, restore the
   SQLite snapshot to a new path and run the documented integrity and schema
   compatibility checks. Open it with the intended binary in the isolated
   recovery boundary. Do not let an older binary silently migrate or discard
   newer desired-state fields. If compatibility is not proven, stop before
   attaching the recovered database to the customer endpoint.
4. **k3s and Kubernetes.** Follow the topology-specific, rehearsed k3s
   recovery method. Restore or rebuild the k3s control plane, kubeconfig,
   server token, namespaces, and cluster-scoped dependencies according to the
   actual datastore mode. Verify API identity, node membership, object
   ownership, PVC attachment, and certificate issuance. No generic datastore
   restore command is prescribed here because the repository does not qualify
   one for every k3s topology.
5. **WireGuard and agents.** Restore the hub and node configuration or
   re-enroll nodes using the approved credential rotation plan. Verify the
   expected peer public keys, routes, heartbeats, and k3s worker status. Revoke
   any credential suspected of exposure and record the replacement.
6. **Applications and data.** Restore Kubernetes objects from the qualified
   source, then restore each application's data using its approved procedure.
   Validate migrations, secret decryption, application reads and writes,
   background jobs, routes, and representative customer checks. For external
   databases, validate the external provider's restore boundary separately.
7. **Traffic release.** Keep the recovered endpoint isolated until health,
   data consistency, certificate, monitoring, and outbound-effect checks pass.
   Then perform a controlled DNS or endpoint cutover and monitor errors,
   latency, reconciliation, and data writes for the agreed observation period.

If a gate fails, keep the recovered environment isolated, preserve logs and
checksums, and record the failed data class. A partial platform restore must
not be presented as application or customer-data recovery.

## Recovery evidence package

For each rehearsal or incident, retain:

- customer and environment ID, recovery boundary, operator, timestamps, and
  approval record;
- backup artifact identifier, checksum, creation time, retention expiry, and
  key reference, with no secret values;
- exact deployer, k3s, ingress, operator, and application versions;
- sanitized configuration manifest and file checksums;
- pre- and post-recovery node, Pod, PVC, route, certificate, and database
  health observations;
- measured RPO, RTO, manual interventions, failed gates, and follow-up owner;
- customer-visible validation and traffic cutover decision.

Review backup age and retention at least daily through an external monitor.
Alert on missing artifacts, failed creation, failed upload or access checks,
expired retention before the next qualified copy exists, and unavailable key
custody. The implementation currently lacks the destination, retention,
key-custody automation, and external alert delivery needed to claim these
controls; keep them as explicit open gates.

## Offboarding and retained data

Before disabling service, export the customer-approved application data and
configuration in a documented format, verify the export, and record who
received it. Revoke deployer admin and agent credentials, remove the customer
CLI context from operator workstations, revoke WireGuard peers, disable public
endpoints and monitoring alerts, and stop update or backup jobs for that
environment.

Create an inventory of retained items before deleting anything: SQLite backup
artifacts, backup encryption keys, secret-encryption and token-hash keys,
audit records, monitoring data, Kubernetes Secrets, PVCs, database backups or
WAL archives, object-storage copies, DNS or certificate records, and host
snapshots. Apply the customer contract and approved retention schedule to
each item. Deletion requires an explicit operator action and a recorded
customer instruction; application deletion must not be assumed to delete
retained backups or database volumes.

After the retention period, destroy data and keys through the approved storage
and key-management procedures, obtain a deletion result for each storage
location, and retain only the minimum audit record required by policy. Record any retention exception, its authority, owner, and next review date.

## Qualification sign-off

An environment is qualified for the POC only when the operator has attached
evidence for onboarding, synthetic recovery, backup age monitoring, key
custody, application data coverage, and offboarding retention handling. The
sign-off must name the exact gaps that remain and the workloads excluded by
those gaps. Until the full-environment rehearsal passes, offer stateless
applications only where their externally stored data has an independently
tested restore path, and keep managed PostgreSQL outside the offering.

Related documents: [hosting operations](hosting-operations.md), [hosting
profile](hosting-profile.md), [hosting namespace](hosting-namespace.md),
[cluster validation](hosting-cluster-validation.md), [update policy](update-policy.md),
and [the POC plan](hosting-poc-plan.md).
