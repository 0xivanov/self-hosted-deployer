# Hosting implementation status

Updated 2026-09-07. The foundation implementation and live legacy-fleet upgrade/rollback rehearsal are complete. All three nodes run the unpublished `v0.3.1-poc.20260906` candidate, with manual update policy and automatic-update timers disabled. Existing workload generations, application Pod UIDs and restart counts remained unchanged. The managed hosting POC acceptance checklist is still open. See [live rehearsal evidence](live-upgrade-rehearsal.md).

## Implemented locally

| Area | Delivered | Remaining qualification or implementation |
| --- | --- | --- |
| P0 compatibility | Synthetic legacy config/rendering fixtures, migration compatibility tests, exact deployed v0.3.0 source comparison, read-only fleet inventory | Server rollback and sequential agent compatibility checks passed; full version matrix and hosted-environment qualification remain |
| P1 release controls | Manual/latest/pinned policy, timer preservation, file snapshots, rollback on failed installation, failure quarantine, disposable installer tests | Manual controls installed and verified on all three nodes; signal/crash recovery and the release installer against a real published artifact remain unqualified |
| P2 contexts | Named customer contexts, protected credentials, endpoint selection guards, mutation target announcements | Verified server identity binding is deferred and fails closed when configured; repeatable separate-customer provisioning is not implemented |
| P3 hosting profile | Opt-in resource and security settings, conservative capacity checks, replica cap, NetworkPolicy, new-namespace budget helper, read-only server preflight | Live image compatibility, runtime exhaustion containment, actual ingress/monitoring and multi-node CNI, separate-environment access tests; capacity admission is not a reservation |
| P4 recovery | Authenticated encrypted consistent SQLite backup and restore to a new path, corruption/wrong-key/no-overwrite tests, synthetic recovery without the original host, opt-in restic delivery with exact-snapshot read-back | B2 configuration/k3s recovery bundles uploaded and downloaded successfully; daily platform SQLite backups and internal age/failure alerts installed; VPS recovery/configuration/k3s and PostgreSQL offsite bundle plus isolated SQL restore passed; worker refresh, independent key custody, retention and full-environment restore remain |
| P5 operations | Redacted mutation audit records, explicit hosting doctor checks, generic replica/restart/resource-budget dashboard, recovery/offboarding runbook | Offsite audit forwarding, external uptime monitoring, delivered alerts, live dashboard validation, exercised onboarding/offboarding and incident runbooks |
| P6 pilots | Qualification plan and acceptance checklist | Independent customer environments, 7-day soak, 30-day pilot and measured costs/support load |

Managed PostgreSQL is excluded from the opt-in hosting profile until its backup and traffic paths are separately qualified. Existing legacy PostgreSQL rendering is unchanged. Billing and customer self-service remain outside this operator-managed POC.

## Validation completed

- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `make build` pass.
- `buf lint`, `make proto-check`, and `buf breaking --against '.git#ref=ec8015fbd825dc658ac3bc72d50ab11cb33208ba'` pass. Existing RPCs and field numbers are preserved; preflight is additive.
- The integration-tagged synthetic recovery drill passes with race detection: deleted source-host directory, recovered hosting state, independently recovered keys, preserved token revocation, and post-restore repository writes. This is not a full-environment RTO measurement.
- ShellCheck passes over `scripts/*.sh` and `tests/*.sh`.
- Release policy and namespace tests pass. Installer tests pass inside a disposable Ubuntu container, including failed restart, fresh-install cleanup, policy restoration, timer preservation, and retained snapshots after injected rollback failure. These are disposable local installer tests; no published artifact installation was exercised.
- The actual generated NetworkPolicy passes a disposable k3s test with baseline, enforcement, and recovery controls. See [network qualification](hosting-cluster-validation.md).
- CI now includes unit/compatibility, race, synthetic database recovery, vet, build, shell, namespace, installer rollback, and protobuf checks. The privileged k3s smoke is an explicit local qualification step.

Read [deployed compatibility](deployed-compatibility.md) for evidence limits. An opted-in environment must not downgrade to v0.3.0, which ignores hosting fields in stored JSON. The known legacy fleet has no adopted hosting profiles.

## Next execution gates

1. Operator-owned VPS accounts, Backblaze B2 and the existing application email alert recipient are confirmed. Establish independent recovery-key custody, retention and recovery targets. Keys currently remain in protected local Mac storage.
2. Daily platform SQLite (03:30 Sofia) and VPS recovery bundles (06:00 Sofia) are enabled with internal failure/staleness monitoring. Confirm their first timer-triggered runs, refresh worker-node configuration coverage, and qualify independent outage monitoring and actual alert delivery. Existing application email alerts are preserved.
3. PostgreSQL recovered successfully from an offsite bundle, including roles and ownership. Remaining qualification covers other volumes, application behavior after a full rebuild, compatible hosted images, runtime resource exhaustion, live ingress/monitoring and separate-customer access. Do not apply a hosting profile to existing apps during legacy rollback testing.
4. The authorized live legacy rehearsal passed on the existing fleet with verified offsite bundles. Keep manual updates enabled until a reviewed release decision. Full host rebuild, automatic installer crash recovery and hosted-state rollback are separate tests.
5. Complete the soak and pilots and record measured outcomes against [the POC checklist](hosting-poc-plan.md).

Final live checks found all three nodes Ready, the deployed services active, fresh worker heartbeats, and the public application readiness endpoint healthy. SQLite remains at schema 6 with a successful integrity check.

The [recovery and offboarding runbook](hosting-recovery-runbook.md) defines data coverage, custody, rehearsal evidence, and retained-data handling. It does not certify any untested recovery path.

The [hosting dashboard](hosting-dashboard.md) has twelve Prometheus-validated panel queries and is additive to existing Grafana dashboards. It displays configured resource budgets, not uncollected live usage. Check and release workflows run the recovery drill and dashboard query validation before artifact publication.

The [local resource/security rehearsal](hosting-resources-validation.md) passed with a healthy nonroot image, CPU quota admission rejection, unchanged healthy Pod UID/restart count, and explicit root-image rejection. This does not establish runtime memory-exhaustion containment or live cluster qualification.

The [backup delivery job](platform-backup-job.md) now has mocked failure tests and a successful real SQLite/restic local-repository round trip. It is now scheduled on the VPS at 03:30 Sofia time; first service run and an isolated Mac restore passed. Backup failure/staleness rules use the existing email route. Retention, independent external monitoring, actual alert delivery and independent key custody remain open. See [scheduled backup evidence](platform-backup-schedule.md). Operator-owned VPS accounts are confirmed.

Backblaze setup: repository initialization and a real three-node recovery-bundle upload completed. Exact-snapshot downloads matched all three original SHA-256 hashes; recovered platform and k3s SQLite databases passed integrity checks, and all 15 stored application secrets decrypted with the recovered platform key. This proves bundle delivery and selected content recovery, not a clean full-host restore or application data recovery. No credential values are included in repository documentation.

The [daily VPS recovery bundle](recovery-backup-schedule.md) adds online platform/k3s snapshots, configuration, installed binaries, and a validated PostgreSQL dump plus role definitions. The offsite PostgreSQL restore recovered 22 tables and 3,232 rows in an isolated container. It does not prove a full host rebuild or business behavior.
