# Hosting implementation status

Updated 2026-09-09. The foundation implementation and live legacy-fleet upgrade/rollback rehearsal are complete. All three nodes now run the unpublished `v0.3.1-poc.20260909` candidate, with manual update policy and automatic-update timers disabled. Existing workload generations, application Pod UIDs and restart counts remained unchanged. The managed hosting POC acceptance checklist is still open. See [September 9 upgrade evidence](live-upgrade-20260909.md) and [the earlier rollback rehearsal](live-upgrade-rehearsal.md).

## Implemented locally

| Area | Delivered | Remaining qualification or implementation |
| --- | --- | --- |
| P0 compatibility | Synthetic legacy config/rendering fixtures, migration compatibility tests, exact deployed v0.3.0 source comparison, read-only fleet inventory | Server rollback and sequential agent compatibility checks passed; full version matrix and hosted-environment qualification remain |
| P1 release controls | Manual/latest/pinned policy, timer preservation, file snapshots, rollback on failed installation, failure quarantine, disposable installer tests | Manual controls installed and verified on all three nodes; signal/crash recovery and the release installer against a real published artifact remain unqualified |
| P2 contexts | Named customer contexts, protected credentials, endpoint selection guards, mutation target announcements | Stable identity binding and guarded base-environment provisioning are implemented locally; fresh Ubuntu VM execution passed on the Mac; provider networking, worker onboarding and complete customer readiness remain unqualified |
| P3 hosting profile | Opt-in resource and security settings, conservative capacity checks, replica cap, NetworkPolicy, new-namespace budget helper, read-only server preflight | Live image compatibility, runtime exhaustion containment, actual ingress/monitoring and multi-node CNI, separate-environment access tests; capacity admission is not a reservation |
| P4 recovery | Authenticated encrypted consistent SQLite backup and restore to a new path, corruption/wrong-key/no-overwrite tests, synthetic recovery without the original host, opt-in restic delivery with exact-snapshot read-back | B2 configuration/k3s recovery bundles uploaded and downloaded successfully; daily platform SQLite backups and internal age/failure alerts installed; VPS recovery/configuration/k3s and PostgreSQL offsite bundle plus isolated SQL restore passed; explicit scoped backup/audit retention helpers are implemented locally; worker refresh, independent key custody, the chosen live retention policy and full-environment restore remain |
| P5 operations | Redacted mutation audit records, explicit hosting doctor checks, generic replica/restart/resource-budget dashboard, recovery/offboarding runbook | Offsite audit collection/export and external HTTPS/certificate/email monitoring are implemented and tested locally; live installation, delivered alerts, dashboard validation and exercised runbooks remain |
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
2. Daily platform SQLite (03:30 Sofia) and VPS recovery bundles (06:00 Sofia) are enabled with internal failure/staleness monitoring. Both first timer-triggered runs succeeded on 2026-09-08. Refresh worker-node configuration coverage, and qualify independent outage monitoring and actual alert delivery. Existing application email alerts are preserved.
3. PostgreSQL recovered successfully from an offsite bundle, including roles and ownership. Remaining qualification covers other volumes, application behavior after a full rebuild, compatible hosted images, runtime resource exhaustion, live ingress/monitoring and separate-customer access. Do not apply a hosting profile to existing apps during legacy rollback testing.
4. The authorized live legacy rehearsal passed on the existing fleet with verified offsite bundles. Keep manual updates enabled until a reviewed release decision. Full host rebuild, automatic installer crash recovery and hosted-state rollback are separate tests.
5. Complete the soak and pilots and record measured outcomes against [the POC checklist](hosting-poc-plan.md).

Final live checks found all three nodes Ready, the deployed services active, fresh worker heartbeats, and the public application readiness endpoint healthy. SQLite remains at schema 6 with a successful integrity check.

The [recovery and offboarding runbook](hosting-recovery-runbook.md) defines data coverage, custody, rehearsal evidence, and retained-data handling. It does not certify any untested recovery path.

The [hosting dashboard](hosting-dashboard.md) has twelve Prometheus-validated panel queries and is additive to existing Grafana dashboards. It displays configured resource budgets, not uncollected live usage. Check and release workflows run the recovery drill and dashboard query validation before artifact publication.

The [local resource/security rehearsal](hosting-resources-validation.md) passed with a healthy nonroot image, CPU quota admission rejection, unchanged healthy Pod UID/restart count, and explicit root-image rejection. This does not establish runtime memory-exhaustion containment or live cluster qualification.

The [backup delivery job](platform-backup-job.md) now has mocked failure tests and a successful real SQLite/restic local-repository round trip. It is now scheduled on the VPS at 03:30 Sofia time; first service run and an isolated Mac restore passed. Backup failure/staleness rules use the existing email route. Retention policy selection, live independent external monitoring, actual alert delivery and independent key custody remain open. See [scheduled backup evidence](platform-backup-schedule.md). Operator-owned VPS accounts are confirmed.

Backblaze setup: repository initialization and a real three-node recovery-bundle upload completed. Exact-snapshot downloads matched all three original SHA-256 hashes; recovered platform and k3s SQLite databases passed integrity checks, and all 15 stored application secrets decrypted with the recovered platform key. This proves bundle delivery and selected content recovery, not a clean full-host restore or application data recovery. No credential values are included in repository documentation.

The [daily VPS recovery bundle](recovery-backup-schedule.md) adds online platform/k3s snapshots, configuration, installed binaries, and a validated PostgreSQL dump plus role definitions. The offsite PostgreSQL restore recovered 22 tables and 3,232 rows in an isolated container. It does not prove a full host rebuild or business behavior.


## Latest implementation and qualification boundary

The September 8 changes add stable server identities with explicit context rebinding, configurable customer WireGuard ranges with the legacy default preserved, guarded base-environment provisioning, offsite audit export, separate full-backup and audit-age retention policies, and an external HTTPS/certificate monitor with SMTP retry/recovery handling. These changes are committed locally and have not been installed on the three live nodes.

Parent review corrected identity publication races, silent context rebinding, incorrect SQLite URI handling, ignored provisioning CIDRs, discarded checksum verification, incomplete provisioning steps, unsafe credential file creation, incompatible journal envelopes, cursor-loss behavior and audit service ordering. The new provisioning workflow is a base control-plane installer, not a complete customer onboarding button: certificate provisioning/renewal, workers, application ingress, offsite scheduling, external alert setup and full restore/isolation qualification remain explicit steps.

Validation includes the full Go suite and race tests, vet, builds, protobuf lint/reproducibility and ShellCheck. Python checks cover actual generated provisioning shell with real isolated file/checksum operations, checksum rejection before binary installation, journal resume/missing-history behavior, a real local restic audit round trip with failed-verification retries, scoped deletion CLI behavior, and real local TLS connections. Provider downloads, systemd/k3s boot and SMTP delivery are mocked where noted; they are not certified by these tests.

Read-only live verification on September 8 confirmed the scheduled platform backup at 03:30 Sofia and recovery backup at 06:00 Sofia both completed successfully with exit status 0. All three Kubernetes nodes were Ready; money-manager-api was 3/3 and money-manager-redis was 1/1 available. No live components were changed during this implementation pass.

The POC is **not yet feature-complete or pilot-qualified**. Next work is to finish and exercise the customer onboarding path on a disposable Linux environment, install and verify audit/external monitoring with real alert delivery, refresh worker recovery coverage, and complete full-environment restore and customer isolation exercises. The seven-day soak and thirty-day pilot remain time-based acceptance gates. Do not mark them complete from local unit tests or from a healthy legacy fleet.

## Mac qualification, September 8

A disposable Ubuntu ARM64 VM on the operator Mac passed all twelve real provisioning stages after fixing a k3s node-registration startup race. The rehearsal uses synthetic credentials, no host directory mounts and no application port forwarding. Real systemd services, k3s readiness, management TLS, namespace quota and encrypted initial backup were checked. A synthetic platform backup was restored on the Mac with SQLite integrity verified; reboot readiness passed in both the repaired and clean rehearsals. See [Mac qualification evidence and limits](mac-qualification.md). No new VPS is needed for this local qualification work.

The Mac app lifecycle rehearsal now passes real CLI preflight/deployment, HTTP ingress, failed readiness containment with the original healthy Pod preserved, explicit rollback, and application readiness after guest reboot. A repeatable guarded test and synthetic image/config are checked in. The guest required native container snapshots after corrupt extracted overlay image files were observed; the cause remains unproven and no live fleet snapshotter was changed. Worker hub persistence is fixed locally to avoid checking the legacy VPN address on custom customer networks. Real worker enrollment, reboot recovery and cross-node ingress now pass in the two-VM lab; see [worker qualification](mac-worker-qualification.md). Public HTTPS and the remaining isolation/recovery gates are still open.

Worker qualification found and fixed a missing WireGuard boot service. The revised installer enables the interface and orders k3s-agent after it. A real repaired-worker reboot returned the node Ready with VPN connected. A test app served through ingress while running on that worker; CLI drain moved it back to the control plane with a brief single-replica HTTP interruption, followed by successful uncordon. No live nodes were changed.

## Clean stateless environment recovery, September 9

A fresh Mac VM restored the synthetic platform/k3s SQLite state, original server identity, tokens, certificates, WireGuard configuration and test app. Verified ingress traffic and the original identity-bound CLI login passed, with the source VM stopped. Parent review added safe archive staging and corrected a lab procedure that had copied private staging-directory modes onto OS parents; the corrected procedure passed from a fresh guest. Recovery snapshots now carry private `0600` modes. See [recovery evidence and remaining limits](mac-recovery-qualification.md). This narrows the full-restore gate for the stateless lab; application data recovery, public DNS/TLS, production offsite full-host recovery, independent key custody and customer isolation still require their own evidence.

Existing-worker recovery also passed after correcting hub peer reconstruction: configured servers now synchronize saved WireGuard peers before accepting RPCs, and the worker reconnected to the replacement with its original agent credential and Kubernetes identity. The new startup path is skipped when worker networking is not configured. The replacement VM is the current lab control plane; never run it concurrently with the retained original.

The replacement then passed a full guest reboot: both nodes Ready, worker VPN connected, app ingress responding and the original operator login working. These changes have only been installed in the disposable lab, not the live fleet.

## Cross-node network enforcement, September 9

The replacement and worker passed a repeatable test of the deployed hosting NetworkPolicy. Unauthorized ingress and egress were blocked through both Pod and Service IPs, with successful before/after controls. Cluster DNS and real Traefik ingress to the protected worker app remained functional. The existing app's Pod identities and restart counts stayed unchanged. See [network qualification](hosting-cluster-validation.md). Independent customer control-plane access, runtime resource exhaustion and public TLS/alert delivery remain open; this test does not certify those boundaries.

Follow-up verification found and fixed missing hosting labels on rendered app Pods. Earlier fixture enforcement did not establish that the actual deployed app matched its policy. After an intentional lab app redeploy, the expanded check verified matching labels and blocked unrelated access to the real app, while ingress stayed healthy. Legacy Pod rendering and immutable Deployment selectors are unchanged; existing opted-in apps need a reviewed redeploy to receive the label. The fix is installed only in the Mac lab.

The same-node memory-exhaustion rehearsal also passed: a disposable container using the actual hosting limits was OOMKilled at its 64Mi limit, while the neighboring app retained its identity/restart count and served every sampled HTTP check. This closes one bounded runtime memory-containment case. CPU/disk exhaustion, independent customer control-plane access and public TLS/alert delivery remain unqualified. See [resource evidence](hosting-resources-validation.md).

## Credential separation, September 9

The integration suite now runs two loopback gRPC servers with independent SQLite token stores and token-hash keys. Each server accepts its own administrator credential and rejects the other environment's credential and missing credentials before the mutation handler executes. This passed with race detection and is included in check/release workflows. It verifies the authenticated RPC boundary, not independent VPS firewalls, TLS issuance or an entire customer provisioning workflow.

## Latest live rollout

The September 9 candidate from `f2ab0d9` is installed on the VPS and both Pi agents. The server was followed by each worker separately, with saved rollback binaries and unchanged application Pod identities/restarts. The first server attempt automatically rolled back after a public-probe timeout; a verified retry passed. This supersedes earlier statements that these server/agent code changes were installed only in the Mac lab. Operational helper scripts, CLI binaries and public alert/certificate qualification were not part of this rollout. See [the live upgrade record](live-upgrade-20260909.md).
