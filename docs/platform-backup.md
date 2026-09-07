# Platform SQLite backup

The server binary has a local operator command for an encrypted SQLite
snapshot. It reads the database through a read-only SQLite connection and
uses `VACUUM INTO` to create a consistent snapshot without running migrations
or changing the source database.

Create a backup with a raw 32-byte key file:

```bash
deployer-server backup create \
  --database-path /var/lib/deployer/deployer.db \
  --output /var/lib/deployer/backups/deployer.backup \
  --key-file /etc/deployer/backup.key
```

The key file must be a regular file containing exactly 32 bytes and must not
be readable by group or other users. The backup uses AES-256-GCM with a random
nonce and authenticated format metadata. The output is created with mode
`0600`, atomically, and an existing output is never overwritten. The command
does not create a missing source database, upload anything, or manage
retention.

Restore only to a new path while the deployer is stopped and no process is
using the destination:

```bash
deployer-server backup restore \
  --input /var/lib/deployer/backups/deployer.backup \
  --output /var/lib/deployer/recovery/deployer.db \
  --key-file /etc/deployer/backup.key
```

Restore refuses an existing destination. It authenticates and decrypts the
backup, runs SQLite `PRAGMA integrity_check`, and publishes the destination
only after that check succeeds. Wrong keys, modified artifacts, and invalid
SQLite payloads leave no destination file.

This is a platform database snapshot primitive. It does not stop application
writers, coordinate Kubernetes or desired-state recovery, validate migration
compatibility with an older binary, or restore over a live database. Operators
must define the recovery boundary, stop writers, and separately verify that
the restored schema is compatible with the binary being used.

The format currently buffers a bounded snapshot in memory and accepts artifacts up to 1 GiB. Use it for small platform SQLite databases; it is not a streaming backup tool for customer databases. Store the backup encryption key separately from the artifact. Recovery of application secrets also requires the original platform secret-encryption key, which is not contained in this database-only artifact. Back up server configuration, WireGuard keys, k3s state, and application data separately.

## Synthetic recovery drill

Run `go test -tags=integration -race ./internal/backup -run TestPlatformRecoveryWithoutOriginalHost -v` to rehearse platform-state recovery using only temporary synthetic data. It backs up an open WAL-mode database, changes the source after the snapshot, closes and removes the synthetic source-host directory, and restores into a separate directory using the encrypted artifact and independently saved backup, application-secret, and token-hash keys.

The drill checks the original recovery point, preserved hosting desired state, secret decryption, rejected decryption with a different key, preserved token revocation, and repository reads/writes after restore. It starts no server, agent, Kubernetes client, or application, and makes no external connection. The reported duration measures only synthetic database recovery and validation, not full-environment RTO. Real offsite delivery, key custody, application-data recovery, k3s recovery, and reconciliation still require the [environment recovery runbook](hosting-recovery-runbook.md).
