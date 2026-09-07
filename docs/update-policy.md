# Host update policy

Each host may have a policy at `/etc/deployer/update-policy.conf`:

```text
mode=manual
```

The supported modes are:

- `manual` disables the automatic updater. An operator can still run the
  installer for an explicit release.
- `latest` follows the latest stable GitHub release. This is the legacy
  behavior when the policy file is absent.
- `pinned` requires an exact release tag and ignores newer releases:

  ```text
  mode=pinned
  version=v0.1.0
  ```

The file is intentionally small and strict. Unknown keys, duplicate keys,
missing values, a non-regular policy path, and an invalid pinned version stop
the installer and updater. A pinned update still has to move forward from the
installed version. Moving a host back to an older binary is a manual rollback
operation and must follow the database compatibility and recovery procedure.

The installer can create or change the policy after a successful artifact
installation:

```bash
sudo deployer-install-release --role server --version v0.1.0 \
  --update-policy pinned
sudo deployer-install-release --role server --version v0.1.0 \
  --update-policy manual
```

`--update-policy manual` without `--version` still downloads the default
`latest` artifact before recording the manual policy. Supply an explicit known
release when the artifact itself must be controlled.

For isolated checks, `DEPLOYER_UPDATE_POLICY_FILE` selects another policy path.
The updater also accepts `--policy-file`; systemd units must be explicitly
configured to pass that option if a non-default path is used. Reinstalling a
release reads and preserves the existing policy. It also preserves a disabled
timer. A new host with no policy keeps the legacy timer behavior; a new host
with `manual` does not enable the timer.

The updater records the effective policy and target version in its output,
keeps a release file snapshot, and quarantines a release after an installation
or health check failure. It restores the previous platform files and policy
file before returning failure. This protects the deployer binaries and systemd
unit files only. It does not make application desired state, Kubernetes
resources, or SQLite schema changes reversible.

The installer now rolls back the targeted platform files, policy, and captured
service/timer state when installation or restart fails. This rollback covers
regular files, absent paths, and systemd mask symlinks. It does not restore
SQLite, application workloads, or Kubernetes resources; schema compatibility
remains a release gate.

Before a platform rollback, stop writers and establish an explicit recovery
boundary. Restoring an old SQLite backup can discard newer operations, and an
older binary may not be compatible with a newer schema. The P1 scripts do not
provide a complete SQLite backup, migration compatibility, or schema rollback
workflow; those checks remain release gates before enabling automatic updates.
