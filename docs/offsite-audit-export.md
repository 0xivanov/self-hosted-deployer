# Offsite mutation audit export

The collector reads the deployer server's retained systemd journal, extracts its structured `mutation audit` events and appends them to a private JSONL file. The exporter validates known methods, caller kinds, outcomes and method-specific target identifiers before uploading each batch to an existing restic repository. Raw request bodies, error text, operational logs, credentials and unknown fields are not exported.

`collect-audit-journal.py` starts with the complete retained journal. Later runs locate the saved cursor inclusively, verify that it is still available, and continue after it. If journal vacuuming has removed that boundary, collection fails visibly. Keep persistent journald storage and sufficient local journal retention for the longest expected exporter outage. A crash after appending but before cursor publication can produce duplicate records; use correlation IDs when reviewing history. The collector fsyncs output before publishing its cursor. Malformed or incomplete local journal files require operator inspection; do not discard them to silence an error.

`offsite-audit-export.py` checkpoints the source inode, offset and prefix hash only after an exact snapshot-ID lookup and byte-for-byte restic download verification. Batches default to 1,000 complete audit records; a partial final line is retried later. Failed uploads or verification keep the checkpoint unchanged. Source rewriting or rotation fails visibly. Snapshots carry the environment and `audit` tags and contain `/audit.jsonl`.

## Opt-in installation

On the control-plane host, install the reviewed scripts and units:

```sh
sudo install -d -m 0755 /usr/local/libexec
sudo install -m 0755 scripts/collect-audit-journal.py scripts/offsite-audit-export.py /usr/local/libexec/
sudo install -m 0644 deploy/systemd/deployer-audit-journal.service deploy/systemd/deployer-audit-export.service deploy/systemd/deployer-audit-export.timer /etc/systemd/system/
```

Set the export service's `--environment` to the correct environment. The provided example uses `legacy-vps`. Configure `/etc/deployer/backup/offsite.env` as a root-owned 0600 systemd environment file with `RESTIC_REPOSITORY`, `RESTIC_PASSWORD_FILE` and provider credentials. The repository must already exist, and the password file must be owner-only. Use a trusted root-owned executable restic binary at `/usr/local/bin/restic`. Never put credential values in the unit or command line.

```sh
sudo systemctl daemon-reload
sudo systemctl start deployer-audit-export.service
sudo systemctl enable --now deployer-audit-export.timer
```

The export service runs collection as an `ExecStartPre` step and proceeds only if it succeeds. Its timer triggers the export service every ten minutes; no service synchronously starts another service that is waiting for it. Inspect both service logs and the timer. Collection and export failures return nonzero and are visible through systemd. These units do not install an email rule for audit-export failures.

The collector creates `/var/lib/deployer/audit` mode 0700 with owner-only output and cursor files. The exporter creates a private work directory. Local log growth is not automatically pruned. For rotation, stop the timer, run successful exports until all complete records are covered, retain the old file with its checkpoint, and create a fresh 0600 audit file with a fresh exporter checkpoint. Preserve the **journald cursor** so subsequent collection resumes correctly. Keep the archived pair until offsite coverage is verified. Do not rotate while either service is running.

## Retention and validation

Audit snapshots are incremental batches, so retaining one snapshot per day would lose records. The retention helper requires a separate age-based policy for audit batches. For example, a dry run with `--backup-kind audit --audit-keep-days 90` retains **every** batch uploaded during the last 90 days and selects only older batches. It rejects calendar-bucket options for audit data. Choose the period explicitly before applying; no audit data is automatically deleted.

Local checks cover journal-envelope extraction, restart/resume, missing history, empty journals and failed queries. A real isolated restic test verifies upload, exact download, partial-line handling and failed-verification retry. No external provider credentials are used by these tests. Installation on the live fleet and monitoring of export failures remain operational qualification steps.
