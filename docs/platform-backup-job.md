# Platform backup delivery job

`scripts/backup-platform-offsite.sh` is an opt-in wrapper around the encrypted SQLite backup command and an existing restic repository. The script itself has no destination defaults, repository initialization or automatic retention deletion. The operator VPS now runs it through the [daily platform backup service](platform-backup-schedule.md), with a successful live B2 round trip and an isolated Mac restore recorded there.

The job requires Python 3.9 or later in addition to the server and restic binaries.

Provide absolute paths for the server binary, restic binary, database, backup key, and work root. The work root must already exist, be owned by the operator running the job, and have mode 0700. The database and backup key must be outside it. The environment tag accepts up to 63 ASCII letters, digits, dots, underscores, and hyphens, starting with a letter or digit.

Set `RESTIC_REPOSITORY` and an absolute `RESTIC_PASSWORD_FILE` pointing to a nonempty, owner-only regular file owned by the job operator. Conflicting `RESTIC_PASSWORD`, `RESTIC_PASSWORD_COMMAND`, and `RESTIC_REPOSITORY_FILE` settings are rejected. Backend credentials, when required, remain in the backend's protected environment configuration, not command-line arguments. The script does not print repository locations, passwords, keys, or raw subprocess logs.

Example after creating and qualifying a repository separately:

```sh
scripts/backup-platform-offsite.sh \
  --server-binary /usr/local/bin/deployer-server \
  --restic-binary /usr/local/bin/restic \
  --database-path /var/lib/deployer/deployer.db \
  --backup-key-file /etc/deployer/backup.key \
  --work-root /var/lib/deployer/backup-work \
  --environment pilot-a
```

Each run creates a private temporary directory, snapshots and encrypts platform SQLite, and streams only that encrypted artifact to restic under the fixed filename `platform.backup`. It parses the full snapshot ID returned by this upload, verifies that snapshot and its environment tag, downloads that exact artifact with the restic cache disabled, and compares its bytes. A missing or ambiguous result, failed upload/read, or mismatch returns nonzero. Success prints the environment and verified snapshot ID; it never selects `latest`.

Temporary artifacts and stage logs are removed on exit. A failed run may already have written a repository snapshot; the job deliberately does not delete it. The environment tag is metadata, not an access-control boundary. Use separate customer repository credentials and storage prefixes with independently verified access separation.

The restic password, SQLite backup key, application-secret encryption key, and token-hash key serve different purposes. Save their recovery references separately. The job uploads neither key files nor server configuration, k3s state, or application data. See [platform backup](platform-backup.md) and the [full recovery runbook](hosting-recovery-runbook.md).

The message `repository round-trip verified` proves only that the configured repository returned the same artifact. A repository can be local. It does not prove offsite placement, retention, disaster recovery, alert delivery, or protection from an operator deleting the repository. Choose and qualify an offsite destination and its retention policy before enabling a schedule. The live schedule has failure, stale-backup and missing-metrics rules using the existing email route. Independent external monitoring and delivery testing remain open.

## Validation

- `python3 tests/platform-backup-job-test.py` exercises success and failed creation, upload, snapshot lookup, read-back, mismatch, malformed/missing summary, wrong tag, missing configuration, and unsafe credential/work-root cases using mocks.
- `python3 tests/platform-backup-job-test.py --real` builds the real server binary and uses a disposable local repository through the official restic 0.18.1 Docker image pinned by digest. All restic containers have networking disabled and mount only the synthetic fixture directory. It verifies the job, retrieves the exact artifact, decrypts it with the server command, and reads a synthetic SQLite row. No real repository or credentials are used.

This uses restic's [backup interface](https://restic.readthedocs.io/en/v0.18.1/040_backup.html) and [restore/dump interface](https://restic.readthedocs.io/en/v0.18.1/050_restore.html). Restic is an explicit operator dependency; the wrapper does not install or update it.
