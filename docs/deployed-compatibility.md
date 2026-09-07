# Deployed v0.3.0 compatibility qualification

The read-only inventory identified the deployed deployer components as commit `ec8015fbd825dc658ac3bc72d50ab11cb33208ba` (`v0.3.0`). The reported Kubernetes runtime is k3s `v1.35.5+k3s1`, with an amd64 VPS control plane and two arm64 Raspberry Pi workers.

I qualified the source against that exact commit in a temporary `git archive` checkout. The main checkout was not switched and no commit was created. The current compatibility fixture tests and sanitized fixtures were copied into the temporary checkout and run there:

```text
go test ./internal/appconfig ./internal/ingress
ok   internal/appconfig
ok   internal/ingress
```

The normalized legacy config fixtures and rendered stateless, resilient, TLS/secret, and PostgreSQL specifications matched the current goldens in the v0.3.0 checkout. This is evidence that the current source changes do not alter those existing rendering paths when `hosting` is omitted. It is source and fixture evidence, not a live cluster or pod UID comparison.

The current source adds an optional `hosting` field to app desired state. The v0.3.0 YAML parser rejects a new `hosting` YAML field as unknown, as expected for an old client. More critically, the v0.3.0 JSON decoder silently ignores an unknown `hosting` field in stored desired state and re-encodes the configuration without it. A v0.3.0 server or runtime must therefore not be allowed to reconcile an opted-in application: capability negotiation or an equivalent preflight gate is required before publishing or applying hosting profiles. Otherwise the old runtime would render the app without the profile's resource, security, and network policy controls.

The old binary was also tested opening a current-schema SQLite database in the temporary checkout. Goose reported version 6 already current, and the v0.3.0 repositories read a representative persisted app row successfully. The migration files and migration versions are unchanged between the deployed commit and the working tree. This verifies opening and reading the current schema with the old source; it is not proof that every old binary operation is safe after a future migration.

The protobuf API adds the read-only `AppService/PreflightApp` RPC and its request/response messages. Existing RPCs and field numbers are unchanged. `buf breaking --against .git#ref=ec8015fbd825dc658ac3bc72d50ab11cb33208ba`, protobuf lint, and generated-code verification pass. A new CLI receives an unsupported-method error from an older server and never substitutes a deployment call.

The qualification found no legacy fixture rendering difference requiring a migration. The material compatibility boundary is the new opt-in hosting profile: it requires a new-runtime gate because old v0.3.0 code cannot preserve or enforce that profile. Existing applications with no hosting block retain the v0.3.0 rendering baseline. Live mixed-version CLI/agent behavior, Kubernetes admission enforcement, live CNI NetworkPolicy enforcement, and upgrade/rollback traffic continuity remain unverified. A separate disposable single-node CNI test is recorded in [hosting-cluster-validation.md](hosting-cluster-validation.md).
