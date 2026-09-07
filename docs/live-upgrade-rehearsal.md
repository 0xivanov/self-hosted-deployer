# Live legacy-fleet upgrade and rollback rehearsal

Completed 2026-09-07 on the operator's existing VPS and two Raspberry Pi workers, with downtime explicitly authorized. No GitHub release was published.

## Build and scope

- Original: `v0.3.0`, commit `ec8015fbd825dc658ac3bc72d50ab11cb33208ba`.
- Candidate: `v0.3.1-poc.20260906`, commit label `90478ef-dirty-e4e4af0b94e2`, built 2026-09-06 16:44:53 UTC.
- Source fingerprint: `e4e4af0b94e2c3c77df86de88afc5d956eea359d7b1f4ec53946a8d377deb907`.
- Server binary SHA-256: `2ec8725eb19d5cd9a3cedcd0e2d7089f74e8f069b63e7e90d09a7bc18473d6ac`.
- ARM64 agent binary SHA-256: `3c315f3df81585175ace2e905cfe102db4397aebd1b48a0c49c24545bf0d2321`.

The Luna review found passive server startup, unchanged migrations through schema 6, unchanged agent code and additive RPC changes. A fresh read-only database check confirmed one active app and zero hosting profiles before downgrade. Separate automated/local compatibility, race, protobuf, installer and isolated-cluster tests are recorded in the implementation status. They are not part of the live installer qualification.

## Recovery evidence

Original binaries, units and update state were saved root-only on every node. The VPS deployer server and k3s were briefly stopped on September 6 to capture consistent platform and k3s state, then restarted successfully. Worker bundles include configuration and original deployer binaries. Bundles do not include application PostgreSQL data or all persistent volumes.

The three bundles were encrypted by restic and uploaded to B2 bucket `0xivanov-deployer-backups`, repository prefix `legacy-vps`, with tag `pre-upgrade-20260906`.

Snapshot: `3aafaac81f91ed0808cc39851ec3cd9710e6c3f9dfd815e4d4fe3a5a246e68d3`.

Each bundle was downloaded from that exact snapshot with restic cache disabled. All downloaded SHA-256 hashes matched their local originals. Selected platform and k3s SQLite files were recovered into isolated local files and passed `PRAGMA integrity_check`. All 15 backed-up application secret ciphertexts authenticated and decrypted using the recovered platform key; plaintext was not logged. The token-hash key was present in recovered configuration.

This is evidence of offsite delivery and selected recovery contents, not a full server rebuild or application data restore. Recovery keys currently reside in protected local Mac storage and still require an independent secure copy. No unattended backup schedule or retention policy was installed by this rehearsal.

## Executed transitions

1. Paused automatic-update timers on all three hosts and verified inactive updater services.
2. Staged architecture-specific candidate binaries and checked their SHA-256 hashes remotely.
3. Replaced the server with the candidate, returned it to the exact original v0.3.0 binary, then installed the candidate again. Readiness and Kubernetes object checks passed after every transition. Authenticated CLI reads succeeded against both server versions.
4. Replaced worker agents sequentially, home first and yasen second. Their k3s-agent services remained active.
5. Installed the reviewed updater/installer scripts and CLI on all three hosts. Set root-owned `mode=manual` policy and left automatic-update timers disabled and inactive. Invoking each updater confirmed it exits without fetching an update.

The rehearsal used staged local binary replacement with restoration of the pre-transition binary on service-readiness failure. It did not exercise downloading a published release through the release installer. No forced failure occurred during the live sequence.

## Final checks and limits

- All three deployer components report the candidate version; k3s remains `v1.35.5+k3s1`.
- All three Kubernetes nodes are Ready and both active worker database heartbeats are fresh.
- All Deployment generations, desired replica counts and available replica counts match the immediate pre-upgrade baseline.
- Application Deployment Pod UIDs and restart counts match the baseline. Completed backup Job Pods are excluded from the readiness comparison.
- The public HTTPS readiness route and authenticated CLI app/node reads succeed.
- Live SQLite integrity passes, schema remains 6, and no hosting profiles were adopted.
- Existing email alert configuration was preserved. No test alert was sent.

These checks do not measure continuous zero downtime, business transaction correctness, full disaster-recovery RPO/RTO, runtime exhaustion containment or a pilot soak. Downgrade to v0.3.0 is prohibited after adopting hosting state because that version drops unknown hosting fields.

Private logs, hashes, inventories, rollback binaries and recovery material are retained outside Git in the maintenance directory identified by `20260906T164453Z`; remote copies are under `/var/lib/deployer/maintenance/20260906T164453Z`. Retain these until the recovery and release follow-up is complete.
