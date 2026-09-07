# Scheduled platform backup

Enabled on the operator's VPS on 2026-09-07. The first service run succeeded, and its exact B2 snapshot was independently downloaded and restored into a separate SQLite file on the operator's Mac.

## Installed schedule and coverage

`deployer-platform-backup.timer` runs daily at **03:30 Europe/Sofia**, with `Persistent=true` to catch a missed run after the host returns. The VPS displays this as 02:30 CEST. Its next scheduled run after setup is September 8. The initial service invocation was manual; the first timer-triggered execution is still pending.

The root service runs the reviewed encrypted SQLite delivery script through `scripts/deployer-platform-backup.py`. It uses a nonblocking lock, a 30-minute timeout, reduced CPU/IO priority, a restricted filesystem view and private temporary files. Each successful run uploads an encrypted artifact and compares it with a download from the exact returned restic snapshot. Failure or interruption preserves the previous successful-backup timestamp.

This schedule covers **platform SQLite only**. The separate [06:00 VPS recovery bundle](recovery-backup-schedule.md) now covers configuration, k3s and a PostgreSQL dump with isolated restore evidence. Other volumes and full environment recovery remain unqualified. The previously verified three-node recovery bundles remain in B2. Nothing in this setup changes the existing application database backup timer or Kubernetes backup Jobs.

Repository: B2 bucket `0xivanov-deployer-backups`, prefix `legacy-vps`, endpoint `https://s3.eu-central-003.backblazeb2.com`. Scheduled snapshots use tag `legacy-vps`. No automatic deletion or prune policy is enabled; retention and storage growth still need review.

The VPS uses restic 0.18.1, downloaded from the official release with the archive SHA-256 checked against the official checksum list. This matches the version used in the isolated backup tests. Installation follows the [official binary instructions](https://restic.readthedocs.io/en/v0.18.1/020_installation.html#official-binaries).

## Protected configuration

- `/etc/deployer/backup/offsite.env`: root-only backend configuration and credentials.
- `/etc/deployer/backup/restic-password`: root-only restic repository password.
- `/etc/deployer/backup/platform-backup.key`: root-only 32-byte artifact encryption key.
- `/var/lib/deployer/backup-work`: private temporary artifacts, normally removed after each run.
- `/var/lib/deployer/backup-status/platform-backup.prom`: atomically published metrics inside a root-only directory.

The Mac retains the existing recovery-key copies. Independent secure key custody, outside both machines, remains an operator follow-up. Do not place keys in Git, logs or the same backup repository. Restoring an encrypted platform artifact needs both the repository password and artifact encryption key; using recovered application secrets also requires the original platform configuration keys.

## Monitoring

`deployer-backup-metrics.service` serves the fixed metrics file at `10.8.0.1:9708/metrics`, bound to the private WireGuard address. It does not serve directories or arbitrary files. Unreadable, malformed or oversized exposition returns HTTP 503.

An additive `deployer-platform-backup` Prometheus scrape job and `platform-backup.yml` rule file were added to the existing ConfigMaps. Existing jobs and rules were retained. Promtool validated the candidate configuration and rules before application; Prometheus then hot-reloaded after both projected files matched, without a Pod restart.

Alerts use the existing default email receiver:

| Alert | Condition |
| --- | --- |
| `DeployerPlatformBackupFailed` | Last completed attempt failed for 5 minutes |
| `DeployerPlatformBackupStale` | Last verified success is over 36 hours old, for 5 minutes |
| `DeployerPlatformBackupMetricsMissing` | Target down/absent or required metrics absent, for 5 minutes |

Rule behavior was tested locally with Prometheus, including healthy, failed, stale and missing-metric cases. Live target/rule health is checked separately. No artificial email alert was sent, so end-to-end delivery for these new alerts is not certified. Monitoring runs on the existing fleet and is not an independent external outage detector.

## First-run evidence

- Exact snapshot: `4ee8684f720ba106c38178773e4c358e3e14d0f91e8cc258ed1685e4c2cbe3ed`.
- Upload and exact-artifact read-back succeeded in approximately 5.6 seconds.
- Systemd reported about 51 MiB peak memory and exit status 0.
- The 167,982-byte artifact was separately downloaded on the Mac, decrypted with the local recovery key and restored to a new path.
- Recovered SQLite passed integrity checks, retained schema 6 and contained one active app.

This is a platform database restore check, not application data recovery, a complete machine rebuild or a measured disaster-recovery RTO.

## Operations

```sh
# Inspect the schedule and last execution without displaying credentials.
ssh deployer-vps 'sudo systemctl list-timers deployer-platform-backup.timer'
ssh deployer-vps 'sudo systemctl show deployer-platform-backup.service -p Result -p ExecMainStatus'
ssh deployer-vps 'sudo journalctl -u deployer-platform-backup.service -n 20 --no-pager'

# Run the same job now, or pause future runs.
ssh deployer-vps 'sudo systemctl start deployer-platform-backup.service'
ssh deployer-vps 'sudo systemctl disable --now deployer-platform-backup.timer'
```

Pausing the timer does not cancel an active job. Stop its service explicitly if needed. A paused schedule will intentionally become stale in monitoring. To remove monitoring, remove only this scrape job and rule file from the current configuration, validate it and reload Prometheus. Do not overwrite unrelated current configuration using an old whole-ConfigMap snapshot.

Installation and verification logs, original monitoring configuration and restic checksum metadata are kept outside Git under the maintenance identifier `20260907-backup-schedule` on the Mac and VPS. No application service was restarted for this setup.
