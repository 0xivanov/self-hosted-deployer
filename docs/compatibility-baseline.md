# Compatibility baseline

This baseline captures behavior from the current source tree for the P0 hosting proof of concept. The fixtures use synthetic names, images, domains, and secret values. They contain no live database, credential, private key, or cluster export.

`internal/appconfig` freezes the normalized JSON produced from four representative legacy configuration files:

- `stateless-default.yaml` covers omitted options and current defaults.
- `resilient.yaml` covers resilient placement and routing.
- `tls-secrets.yaml` covers public routing, secret references, and a TLS-enabled ingress input.
- `postgres.yaml` covers managed PostgreSQL defaults and immutable image metadata.

`internal/ingress` renders a deterministic bundle for each corresponding scenario and compares its readable JSON fixture. The bundle includes the Deployment, Service, PodDisruptionBudget, and, where applicable, the Ingress, application Secret, and PostgreSQL Cluster. The fixture values are sanitized. A fixture change means the rendered Kubernetes specification needs review before a compatibility-sensitive release; it does not by itself prove that a live cluster would roll out.

`internal/db/migration_compatibility_test.go` creates a disposable SQLite database at migration 1, inserts representative pre-upgrade app, route, and node state, migrates through the current Goose migrations, and reads that state through the repositories. It also exercises old-style column reads while the upgraded schema is present, rolls the schema back to migration 1, verifies the app row survives, and reapplies all migrations. This establishes source-level migration and persisted-state behavior for the current migration set.

Run the baseline with:

```sh
go test ./internal/appconfig ./internal/ingress ./internal/db
```

The checks are disposable fixture tests. They do not inventory or contact deployed versions, validate a real Kubernetes API, compare pod UIDs, or prove mixed-version CLI/agent interoperability. Those checks require an explicitly provisioned disposable environment and an exact version matrix from the deployment inventory.
