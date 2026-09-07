# VPS recovery bundle and application database restore

Enabled on 2026-09-07 after a live B2 upload, exact-snapshot download and isolated PostgreSQL restore. This supplements the [03:30 platform SQLite backup](platform-backup-schedule.md).

## Schedule and contents

`deployer-recovery-backup.timer` runs daily at **06:00 Europe/Sofia**, with missed-run catch-up enabled. This follows the existing application PostgreSQL dump timer, which runs at 02:15 UTC with up to 15 minutes of delay. Existing application dump retention and Pi backup Jobs were preserved.

The bundle contains:

- Consistent SQLite online snapshots of the platform database and k3s datastore, placed at their original relative restore paths. Live database, WAL and SHM files are excluded from raw directory capture.
- Deployer, WireGuard, k3s, TLS and PostgreSQL configuration, k3s server token, relevant service units and the installed deployer/backup binaries and scripts.
- The most recent finalized application PostgreSQL custom dump, rejected if missing, empty or older than 36 hours. It is copied into private staging, checked for source changes and validated with `pg_restore --list` before archiving.
- Fresh PostgreSQL global role definitions, including ownership and password-hash information needed for recovery.

The active backend credentials, repository password and artifact encryption key directory `/etc/deployer/backup` are excluded. Required platform configuration and token files are checked for changes during capture. TLS certificate/key targets are included with the certificate tree. A future change to configured key paths requires updating the recovery manifest.

The job does not stop production services. SQLite consistency is per database; platform, Kubernetes and PostgreSQL captures are not one atomic cross-system snapshot. The PostgreSQL data timestamp is the dump creation time, not the time the bundle uploads.

K3s requires both the datastore and original server token for recovery. See [K3s backup requirements](https://docs.k3s.io/datastore/backup-restore). This job snapshots the live SQLite databases using SQLite's online backup API instead of copying an actively changing database/WAL pair.

## Service isolation and failures

The first live attempt failed because the hardened root service could not switch to the postgres account. No success was recorded. The completed implementation uses a separate `deployer-postgres-globals-backup.service`, running as `postgres`, to atomically write a private globals file. The root bundle job starts that service through local systemd IPC and reads its output only after successful completion. The main filesystem restrictions remain enabled.

The existing wrapper serializes runs and publishes success, failure, duration and last-success metrics in `/var/lib/deployer/recovery-backup-status`. The globals helper preserves its previous output on command failure or empty output; a capture failure still fails the enclosing bundle run rather than accepting that old file.

The fixed metrics endpoint is bound to private WireGuard address `10.8.0.1:9709`. The additive `deployer-recovery-backup` Prometheus job has failure, 36-hour staleness and missing-metric rules routed through the existing email receiver. Local Prometheus rule tests cover healthy and failing cases. No artificial email was sent.

B2 repository prefix remains `legacy-vps`; bundle snapshots carry tags `legacy-vps` and `recovery`. A successful run downloads `/recovery.tar.gz` from the exact returned snapshot and compares bytes. There is no automatic snapshot deletion or prune policy.

## Completed recovery checks

Snapshot: `930934886e4d625b6de641baed4cdd733d40a7abbe424b77f152f9912ec74f07`.

- The uploaded bundle was downloaded separately onto the Mac, approximately 57.7 MB compressed.
- Both recovered SQLite databases passed integrity checks. Each database had exactly one archive entry and no live WAL/SHM companion.
- The server token and platform configuration were present; the backend credential/key directory was absent.
- The application dump and globals were recovered from this offsite snapshot.
- PostgreSQL 17.10 was started in a disposable container with networking disabled, no host ports, temporary database storage and only the recovered dump mounted read-only.
- Globals were restored, followed by the database with ownership and privileges enabled and `--exit-on-error`. Only the duplicate creation statement for the already-existing bootstrap `postgres` role was omitted; its remaining attributes were restored.
- The recovered database contained 22 tables and 3,232 public-schema rows, owned by the expected application role, with zero unvalidated constraints. No business record values or password hashes were logged.
- The restore and checks took approximately three seconds after the test image was available. This is not a complete incident-recovery RTO.

The test image was `postgres@sha256:7958605b474b3d264a969cb3a123d6aa00ad1e1fe9da8a69984dabb704d93317`. Restore behavior follows the [PostgreSQL 17 pg_restore interface](https://www.postgresql.org/docs/17/app-pgrestore.html). Production PostgreSQL and application workloads were not replaced or restarted.

## Remaining qualification

The first timer-triggered execution is still pending after initial setup. Check both daily timers on the next run. Independent key custody, deliberate retention limits, an external outage detector and a complete host rebuild remain open. Worker-node configuration has the earlier verified offsite bundles but is not refreshed by this VPS schedule. Redis persistence and other application volumes are not included in this PostgreSQL recovery proof. Application business behavior after a full environment rebuild and a pilot soak also remain unqualified.

Live configuration is root-only at `/etc/deployer/backup/recovery.json`. Private installation and recovery evidence is retained under maintenance identifier `20260907-application-recovery` on the Mac; original monitoring configuration is under `20260907-recovery-monitoring` on the VPS.

```sh
ssh deployer-vps 'sudo systemctl list-timers deployer-platform-backup.timer deployer-recovery-backup.timer'
ssh deployer-vps 'sudo systemctl show deployer-recovery-backup.service -p Result -p ExecMainStatus'
ssh deployer-vps 'sudo systemctl start deployer-recovery-backup.service'
```

Stop or disable these units only through a planned operational change. Stopping the daily schedule intentionally causes stale-backup alerts. A rollback to a server binary without the `backup` command also breaks the separate platform backup job; retain a compatible backup tool during such a rollback.
