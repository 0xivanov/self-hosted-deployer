# Backup retention planning

`scripts/backup-retention.py` produces a dry-run plan by default. It scopes
the plan to snapshots carrying both the exact environment tag and either the
exact backup-kind tag or `backup-kind:<kind>`. For full backups, daily, weekly and monthly
counts must all be positive. Calendar boundaries are evaluated against the
explicit UTC `--now` value, making plans deterministic in tests and reviews.

Pre-upgrade and rollback snapshots are always retained. Applying a reviewed
plan requires `--apply`; the command passes only the listed eligible snapshot
IDs to `restic forget`. It never invokes a broad retention policy. `--prune`
is a separate explicit option and requires `--apply`.

```sh
python3 scripts/backup-retention.py \
  --restic-binary /usr/local/bin/restic \
  --environment legacy-vps --backup-kind recovery \
  --daily 7 --weekly 4 --monthly 12 \
  --now 2026-09-07T00:00:00Z
```

New platform uploads carry a `platform` tag in addition to the environment tag. Older snapshots without a backup-kind tag are not selected or deleted. Future-dated snapshots are retained. Audit snapshots contain separate incremental batches; the helper refuses calendar-bucket `audit` retention because thinning those batches would lose history.

`--prune` reclaims unreferenced repository data globally after explicit snapshot deletion. It does not delete snapshots retained for other environments, but it may reclaim data already orphaned by other operations. No retention timer is installed, and the example counts are an operator choice, not an automatically applied policy. Re-run and inspect a dry run before applying; application recalculates the candidates from the current repository.

For audit batches, use a separate upload-age policy. This retains all batches in the period, including multiple batches per day. It measures age from snapshot upload time, so delayed journal collection does not shorten retention. Protected pre-upgrade and rollback snapshots remain retained. This example is a dry run:

```sh
python3 scripts/backup-retention.py --restic-binary /usr/local/bin/restic \
  --environment legacy-vps --backup-kind audit --audit-keep-days 90
```

Do not pass `--daily`, `--weekly` or `--monthly` for audit retention. Add `--apply` only after reviewing the selected IDs and approving that retention period.
