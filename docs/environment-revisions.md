# Immutable application environment revisions

Status: implemented in source, not installed on the live VPS or Pi workers. Existing applications using `secrets:` retain their current behavior.

An environment revision is an immutable, encrypted map of application environment variables, scoped to one app name. Operators may stage a revision before the app's initial deployment. App configuration stores only a 64-character lowercase hexadecimal revision ID:

```yaml
name: my-api
image: ghcr.io/example/my-api@sha256:REPLACE_WITH_ARM64_IMAGE_DIGEST
environmentRevision: REPLACE_WITH_64_CHARACTER_HEX_REVISION
```

The normal service, routing, placement and hosting settings are still required. `environmentRevision` and legacy `secrets` cannot be used together. Omitting both removes environment references from the next deployment; an empty saved map is also a valid revision.

## Operator interface

`deployer environment create --values /absolute/private/values.json my-api REVISION`

The file is a flat JSON object of variable names to string values. It must be a regular file owned by the caller with mode 0400 or 0600. The CLI refuses symlinks, duplicate keys, trailing JSON and files larger than 256 KiB. Values do not appear in command arguments or command output. Output contains only app name, revision, sorted variable names and creation time. The RPC is admin-only; there is no plaintext getter.

Each revision permits up to 64 variables. Names follow `[A-Za-z_][A-Za-z0-9_]*`, with a maximum of 128 bytes. Values permit empty strings and UTF-8 text, including newlines, but no NUL bytes. Each value is bounded to 8192 bytes, and all names and values together to 32768 bytes. Each app may retain 100 revisions. Repeating a revision with identical values is idempotent; different values require a new ID.

## Publication and restoration

Migration 8 adds `environment_bundles`. Ciphertext binds its purpose, app name and revision so copying a row cannot rebind secrets to another application. Deploy and preflight resolve the revision before app/deployment writes. Failed-update rollback resolves the original configuration's revision.

Kubernetes receives a separate app-owned, immutable Opaque Secret for each environment revision. Pods reference it through `envFrom`; deployment manifests and status contain no values. Updating an app never overwrites its old environment Secret. This lets existing pods restart using their original settings during an update, and allows a retained release to restore its matching values. Image pull credentials remain separate Docker registry Secrets.

Final app deletion removes owned environment Secrets only after the other app resources have been removed, then deletes encrypted bundle records. Cleanup validates ownership, revision and generated name, and uses deletion preconditions. Individual revision deletion and orphan staging cleanup remain unfinished; do not manually remove a revision still needed for a retained release.

Back up the deployer database and its existing encryption key together with the normal secure recovery procedure. The customer portal's environment vault uses a separate shared portal/fleet key. Neither key belongs in Git or logs.

## Evidence and remaining work

Local service tests cover staged creation, authorization, immutable retries, ciphertext binding, missing revisions before writes, value injection, failed-update restoration and app cleanup. Fake-Kubernetes checks cover public/private image compatibility, version rotation, restoration, empty settings, reference removal, value-free deployment objects, immutable mismatch rejection and cleanup. Portal browser verification used synthetic values in a disposable database.

No real application image was run for this feature. A controlled ARM64 fleet deployment and restore still need qualification, together with Docker rollout-failure recovery. Install matching core server/CLI and all portal database consumers as a coordinated rollout before enabling Docker hosting.
