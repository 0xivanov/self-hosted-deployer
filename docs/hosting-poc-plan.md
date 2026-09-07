# Managed Hosting POC Upgrade Plan

Status: proposed implementation plan, 2026-09-06. No production changes are authorized by this document alone.

## Goal and scope

Use Self-Hosted Deployer to operate a small managed hosting business. Prove that one operator can onboard, maintain, recover, and offboard three pilot customers with predictable effort while preserving existing deployments.

Planning assumptions:

- The operator performs deployments and support. Customers do not receive platform admin tokens, Kubernetes access, or arbitrary container execution in a shared cluster.
- Each customer receives a separate datacenter VM or dedicated cluster, with its own deployer server, state database, credentials, WireGuard network, and workloads. Customer environments do not join the existing three-node cluster or share its ingress. A single VM tier has explicitly limited availability.
- The existing three nodes remain the legacy environment. Their actual roles, software versions, capacity, storage, and workload state must be discovered before rollout.
- Initial workloads are reviewed stateless HTTP applications, with external databases or explicitly supported PostgreSQL configurations. Local persistent data requires a tested application-specific backup and restore procedure before onboarding.
- Billing and customer onboarding can be manual. A public dashboard, shared-cluster tenancy, customer RBAC, usage billing, multi-cluster scheduling, and automatic infrastructure purchasing are outside this POC.
- Independent customer environments are managed through CLI contexts. This does not require one central deployer server to orchestrate multiple clusters.

These assumptions deliberately bound the first commercial offering. Supporting unrelated customers inside the current cluster would require a separate tenancy design and security review.

## Current implementation and constraints

| Area | Repository evidence | POC implication |
| --- | --- | --- |
| Authentication | `internal/server/auth.go` distinguishes admins and agents | Keep access operator-only; never reuse an admin credential between customers |
| Persistence | `internal/db/db.go` opens SQLite and runs Goose migrations at startup | Upgrade and binary rollback must be tested against real schema versions |
| App configuration | `internal/appconfig/config.go` rejects unknown YAML fields; app desired state is JSON in SQLite | New options must be optional; old clients must not silently erase new settings |
| Kubernetes runtime | `internal/ingress/controller.go` binds clients to one configured namespace | Preserve existing namespace and object identities; do not retrofit customer namespaces into the legacy environment |
| Deployment operations | `internal/server/app.go`, `internal/ingress/`, and CLI commands implement reconciliation and lifecycle operations | Extend existing paths instead of adding a second deployment engine |
| Updates | `scripts/auto-update.sh` follows the latest GitHub release; installer enables update timers by default | Contain the existing update mechanism before publishing a new stable release |
| Monitoring | `scripts/install-monitoring.sh`, `deploy/monitoring/` | Add generic customer dashboards and alert delivery checks; current defaults include a discard alert receiver |
| Database recovery | `docs/postgres-ha.md` explicitly requires independent backups and restore drills | HA is not backup; package an operable recovery workflow |

This is source inspection, not evidence of the live fleet's configuration or a completed security audit.

## Compatibility rules

1. Existing `deployer.yaml` files, CLI commands, tokens, server addresses, node identities, and environment variables keep working with unchanged defaults.
2. Do not rename or recreate existing Kubernetes objects, namespaces, PVCs, database clusters, or route domains. Preserve selectors, secret revisions, and ownership rules.
3. An upgrade with no desired-state changes must cause no application rollout, route interruption, database maintenance, or workload rescheduling. Compare object specifications and pod UIDs before and after several reconciliation cycles.
4. New resource and security policies are explicitly selected for new customer environments. Omitted settings retain legacy rendering. Do not introduce cluster-wide admission or networking changes into the current cluster.
5. Use additive protobuf fields and RPCs, retaining field numbers and existing method semantics. Advertise capabilities; reject unsupported new-feature requests before any mutation.
6. Support the inventoried deployed CLI and agent versions against the new server. Also test the new CLI's legacy commands against the deployed server. Establish an explicit supported version matrix rather than assuming all older releases work.
7. Only additive, backward-compatible schema migrations enter this POC. Test the previous server opening and operating on the upgraded schema; Goose version handling must be verified, not assumed. If this fails, the migration cannot ship through automatic binary rollback.
8. Preserve new desired-state fields when legacy clients perform edits. When preservation is impossible, reject the operation with an upgrade message before writing. Test deploy, scale, rollback, and secret-triggered reconciliation specifically.
9. Pin k3s, Traefik, cert-manager, CloudNativePG, and monitoring versions during this work. Their upgrades and any existing database migration are separate maintenance tasks.
10. Release only after upgrade and rollback checks pass. A green process health endpoint alone is insufficient evidence of application health.

## Implementation work packages

Each numbered item should be a separate reviewable change, with smaller PRs where necessary. Implement in order; optional database hosting follows the core stateless POC.

### P0. Capture the compatibility baseline

**Code areas:** `internal/appconfig/`, `internal/ingress/*_test.go`, `internal/db/*_test.go`, `scripts/smoke-test.sh`, CI configuration.

- Inventory current release commits, node roles, architecture, timers, Kubernetes versions, namespaces, app configurations, desired replica counts, ingress, storage, and backup status through a read-only runbook.
- Record sanitized fixtures for existing configurations and Kubernetes object specifications. Never commit live databases, secret values, environment files, or private keys.
- Build a disposable environment matching the deployed versions and topology where relevant. Use generated secrets and representative data.
- Add legacy config parsing, manifest compatibility, migration compatibility, and mixed-version CLI/agent integration checks. Cover TLS, secrets, resilient placement, retained PostgreSQL data, and ownership guards.
- Record the existing test results before changes. Distinguish pre-existing failures from regressions.

**Acceptance:** fixtures reproduce current behavior; the compatibility matrix names exact versions; tests detect an unexpected rollout or object rename. No live mutation is needed for this package.

### P1. Make upgrades controllable and rollback safe

**Code areas:** `scripts/auto-update.sh`, `scripts/install-release.sh`, `deploy/systemd/`, `cmd/deployer-server/`, `.github/workflows/release.yml`, `docs/operations.md`.

- Add a persistent host update policy with `manual`, `pinned`, and existing `latest` behavior. Preserve legacy behavior when no policy exists; new hosting installations explicitly select manual or pinned mode.
- Make installer and updater respect the same policy. Reinstallation must not re-enable a disabled timer or discard a pin. Show effective version and policy in diagnostics.
- Add preflight checks for supported version transitions, disk space, schema compatibility, and required backups before replacement. Serialize manual and automatic installation as today.
- Package a consistent SQLite backup method and a migration/rollback compatibility check. Preserve existing installer recovery and quarantine behavior.
- Verify release artifacts in a disposable environment with the previous release installed. Keep candidate releases as prereleases until promotion gates pass.
- Document rollback separately for platform binaries, app desired state, Kubernetes resources, and database recovery. Restoring a stale platform database can discard newer operations; use it only with writers stopped and an explicit recovery boundary.

**Acceptance:** pinned hosts ignore newer releases; reinstalls preserve policy; failed update returns to the previous working release; previous binaries operate on the allowed upgraded schema; no customer data is reset by rollback.

**Bootstrap safety:** the old updater cannot honor a new pin setting. Before publishing any stable release from this work, inspect and disable/stop the existing timers on all three nodes in an agreed maintenance session, verify no update service or install lock is active, and record versions. `--no-enable-timer` alone does not disable an already active timer. Resume only under the verified policy.

### P2. Add explicit customer environment selection

**Code areas:** `internal/cli/config.go`, `internal/cli/client.go`, `cmd/deployer/login.go`, `cmd/deployer/options.go`, new CLI context commands, operator documentation.

- Add named contexts containing endpoint, credential reference, environment ID, and customer label. Keep credentials protected with existing secure local file behavior, strengthening permissions where needed.
- Preserve the existing single-endpoint configuration as a legacy default. Do not contact or rewrite other environments during migration.
- Add `contexts list`, `contexts use`, and per-command `--context`; explicit selection wins over the default. Display target context in mutating command output, without breaking machine-readable output contracts.
- Bind a context to the expected server identity so an accidental endpoint change fails before a mutation. Introduce this only with capability negotiation for legacy servers.
- Create a repeatable customer installation checklist with separate credentials, certificates, network ranges, backup locations, resource budgets, and pinned versions. Invoice manually outside the deployer.

**Acceptance:** two disposable customer environments can host the same app name; an operation on A changes nothing on B; unknown contexts fail closed; old login/configuration still works.

### P3. Add opt-in hosting workload policies

**Code areas:** `internal/appconfig/`, `internal/ingress/ingress.go`, `internal/ingress/resources.go`, `internal/ingress/controller.go`, `internal/server/app.go`, protobuf sources and generated output where needed.

- Introduce a versioned, explicit hosting profile persisted with desired state. Define CPU/memory requests and limits, ephemeral storage limits, replica limits, and environment capacity budgets.
- Implement capacity preflight with headroom for rolling surges, system services, monitoring, and database workloads. Reject insufficient capacity before changing the running application.
- For compatible customer images, require non-root execution, no privilege escalation, dropped capabilities, a runtime-default seccomp profile, and disabled automatic service-account token mounting. Make writable paths explicit. Reviewed exceptions must be recorded.
- Add narrowly scoped network policies in new customer environments. Enumerate DNS, ingress, monitoring, database/operator traffic, and application outbound dependencies. Verify enforcement with the installed network implementation before claiming isolation.
- Add quotas/limit ranges only to new hosting namespaces. Ensure rollout surge and maintenance jobs fit their limits. Preserve existing runtime ownership protections.
- Add a read-only preflight report showing proposed policy, capacity issues, and affected objects before applying a profile. Do not silently adopt existing applications.

**Acceptance:** legacy fixtures render unchanged; an opted-in test app stays healthy; a resource-exhaustion test is contained; forbidden network paths fail while required paths work; incompatible images are rejected or explicitly excepted. Separate customer VMs/clusters remain the primary isolation boundary.

### P4. Make recovery an operated feature

**Code areas:** new backup/restore scripts or commands, `internal/db/`, `cmd/deployer/doctor.go`, `docs/operations.md`, `docs/postgres-ha.md`, monitoring rules.

- Provide encrypted off-host backups of consistent platform SQLite state and the required configuration. Back up encryption keys and essential credentials through a separately protected recovery path. Checksums alone are not encryption.
- Document recovery of k3s datastore, server token, WireGuard identity/configuration, TLS configuration, and retained volumes for the actual installed topology. Do not assume etcd snapshots work for a single-server SQLite k3s installation.
- Set explicit retention and backup-age alerts. Use per-environment storage credentials and backup prefixes; verify access separation.
- Implement restore to a clean isolated environment with outbound effects disabled until validation. Verify secret decryption, desired-state reconciliation, application reads/writes, and consistency using representative data.
- For PostgreSQL hosting, select and pin a supported CloudNativePG backup integration during implementation, automate backups/WAL handling as applicable, and test point-in-time recovery. Until this passes, offer external databases only.
- Define each application's backup coverage, maximum acceptable data loss (RPO), and recovery time (RTO). Suggested internal POC targets: platform state RPO at most 24 hours and full small-environment recovery within 4 hours; PostgreSQL RPO at most 15 minutes if offered. These are test targets, not customer guarantees.

**Acceptance:** a clean restore succeeds without access to the failed host; measured recovery meets the chosen targets; stale backups alert; backup failure is visible; deleting an app does not automatically delete retained backups or database volumes.

### P5. Add service operations and onboarding checks

**Code areas:** `scripts/install-monitoring.sh`, `deploy/monitoring/`, `internal/server/event.go`, `cmd/deployer/doctor.go`, new customer onboarding/offboarding runbooks.

- Provide generic per-environment app, capacity, certificate, rollout, and backup dashboards. Keep existing custom dashboards working.
- Add an external HTTPS availability probe so VPS or cluster failure remains visible when local monitoring is down. Configure and test a real alert receiver for each customer environment.
- Extend doctor/preflight to expose missing backup coverage, unsupported versions, low capacity, certificate trouble, and update policy. Do not change legacy command exit semantics accidentally; provide an explicit hosting-readiness mode.
- Record operator mutations with actor/token identifier, context/environment, target, time, outcome, and correlation ID. Never record raw tokens, secret values, or sensitive request bodies. Keep an off-host copy with a defined retention period.
- Document deploy, failed deploy, recovery, certificate renewal, credential revocation, and capacity expansion procedures. Support hours and incident escalation must be explicit.
- Add offboarding export and credential-revocation steps. Retention/destruction of data requires an explicit operator action and a recorded customer instruction.

**Acceptance:** an external outage and a failed backup produce actionable notifications; a second operator can follow the recovery runbook; all destructive operations identify the target environment; no secret appears in logs or exports intended for sharing.

### P6. Qualify the release and run the pilot

**Dependencies:** P0 through P5. PostgreSQL qualification is required only for the PostgreSQL offering.

- Run unit/integration checks, `make vet`, `make build`, and protobuf lint/reproducibility checks when contracts change. Add a real disposable-cluster suite because fake Kubernetes clients cannot establish network enforcement, scheduling, or traffic continuity.
- Run the exact old-to-new upgrade, new-to-old rollback, failed-migration, mixed-version, and interrupted-install scenarios. Exercise existing app deploy/scale/rollback/secrets/logs and new profiles.
- For stateless replicas, test worker loss, image pull failure, bad readiness, capacity exhaustion, and return of a stale node. Test edge/server loss separately as a recovery scenario; do not claim single-VPS HA.
- Complete a seven-day disposable-environment soak including an upgrade, rollback, backup failure, and restore. Record error rate, latency, resource usage, incidents, and manual interventions.
- Upgrade the existing environment only through the sequence below. Start customers in separate environments, then observe the three-customer pilot for 30 days before expanding scope.

**Acceptance:** all compatibility gates pass; customer isolation and recovery have evidence; no unexplained upgrade-induced rollout or traffic regression remains; operator effort and direct costs are recorded per customer.

## Existing three-node rollout procedure

1. Obtain SSH connection instructions when implementation reaches live inventory. Establish actual roles and current health using read-only commands; do not assume all three nodes are workers or that spare capacity exists.
2. Before stable release publication, arrange the update freeze described in P1. Record timer state for later restoration. Take verified consistent backups, securely preserve keys, and retain the exact installed artifacts/checksums.
3. Run external continuous HTTPS probes and representative application checks. Record pod UIDs, generations/spec hashes, endpoints, database health/replication, routes, and resource pressure. Fix pre-existing degradation before upgrading.
4. Rehearse the exact transition and rollback in the disposable environment. Confirm the old agents work with the new server and the new CLI with both versions.
5. Install the pinned candidate on the server only, leaving agents unchanged. Do not restart k3s, WireGuard, ingress, or database services as part of a deployer binary upgrade. Verify multiple reconciliation cycles and application-level checks.
6. If an agent change is required, update one eligible node after verifying role and capacity. An agent upgrade must not rerun enrollment, reinstall k3s, or drain nodes automatically. Check it before advancing one node at a time.
7. Observe the server-only and first-agent stages for at least 30 minutes each; observe the completed fleet for 24 hours. Keep the new hosting profile disabled on all legacy apps.
8. Stop rollout immediately for unexpected object changes, pod replacement, unhealthy routes, secret failures, database degradation, missing heartbeats, or a sustained increase over baseline errors. Roll back only the changed component using the rehearsed compatibility path; preserve evidence and keep updates frozen.
9. After the observation period, retain version pins. Resume automation only if it honors pins and has passed the same checks. Do not publish a later stable release assuming fleet safety without rechecking that protection.

No plan can guarantee zero failures before live inventory and rehearsal. The release acceptance criterion is no disruption to existing application traffic or data from the deployer upgrade; a brief management API restart may occur. If inspection shows the topology cannot satisfy this, stop and design a specific maintenance path before changing it.

## POC completion checklist

- [ ] Existing deployments pass unchanged-config upgrade and rollback checks.
- [ ] All three existing nodes have known versions and verified update policy.
- [ ] Three separate customer environments can be provisioned repeatably.
- [ ] Customer A cannot use its credentials or workload network to reach customer B's management plane or private data.
- [ ] Every hosted app has an explicit resource budget, backup scope, recovery target, and support boundary.
- [ ] External outage alerts, certificate checks, backup alerts, and mutation records work.
- [ ] A clean environment restore passes the measured RPO/RTO targets.
- [ ] Onboarding, ordinary deployment, failed deployment, and offboarding are documented and exercised.
- [ ] Thirty days of pilot data records availability, incidents, support hours, and infrastructure/backup costs per customer.
- [ ] Pilot revenue covers measured direct costs and operator time at the chosen rate before expanding.

## Decisions to settle before implementation reaches the relevant gate

- P0: SSH access method, exact node roles, current production workloads, and acceptable maintenance timing.
- P2: operator-owned VPS accounts confirmed; datacenter region and first supported application type remain to be selected.
- P4: backup destination, retention, key custody, and per-app recovery targets.
- P5: support hours, alert destination, and incident escalation contact.

These do not prevent starting compatibility fixtures and release-control implementation. They must be resolved before provisioning, configuring external services, or changing live nodes.
