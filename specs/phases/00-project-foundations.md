# Phase 00: Project Foundations

Goal: create the repository shape, shared conventions, and local development loop before implementing platform behavior.

## 00.01 Choose Initial Language And Module Layout

Goal: establish the implementation layout.

Inputs:

- Main spec requirement for a Go CLI.
- Need for CLI, API server, and agent binaries.

Implementation Notes:

- Create a Go module.
- Implement the CLI in Go.
- Implement the server and agent in Go for shared types, build tooling, and deployment simplicity.
- Use a monorepo with separate binaries:
  - `cmd/deployer`
  - `cmd/deployer-server`
  - `cmd/deployer-agent`
- Use shared internal packages:
  - `internal/api`
  - `internal/proto`
  - `internal/config`
  - `internal/logging`
  - `internal/security`
  - `internal/repository`
  - `internal/version`

Acceptance Criteria:

- `go test ./...` runs successfully.
- `go run ./cmd/deployer --help` works.
- `go run ./cmd/deployer-server --help` works.
- `go run ./cmd/deployer-agent --help` works.

Dependencies: none.

Out Of Scope:

- Real API behavior.
- Real persistence.

## 00.01A Add Protobuf Directory And Tooling Layout

Goal: establish where protobuf source files live and how generated Go code is managed.

Inputs:

- gRPC/protobuf API decision.
- Go monorepo layout.

Implementation Notes:

- Store protobuf source files under:
  - `proto/deployer/v1/*.proto`
- Store generated Go protobuf code under:
  - `internal/proto/deployer/v1`
- Keep protobuf source files as the API source of truth.
- Generated files should be reproducible from source with one command.
- Add protobuf tooling config:
  - `buf.yaml`
  - `buf.gen.yaml`
- Prefer Buf for linting and code generation consistency.
- Add build commands:
  - `make proto`
  - `make proto-lint`
  - `make proto-check`
- `make proto` regenerates Go code.
- `make proto-lint` validates protobuf style.
- `make proto-check` fails when generated code is stale.
- Commit both `.proto` source files and generated Go files for simpler builds in fresh clones.
- Do not edit generated Go files by hand.

Acceptance Criteria:

- `proto/deployer/v1` exists.
- `internal/proto/deployer/v1` is the documented generated-code target.
- Buf config exists.
- `make proto` generates Go protobuf and gRPC code.
- `make proto-lint` runs successfully.
- `make proto-check` detects stale generated files.
- Documentation states that `.proto` files are source of truth.

Dependencies:

- `00.01`

Out Of Scope:

- Publishing protobuf packages for other languages.
- Backward compatibility policy beyond initial `v1` package naming.

## 00.02 Add Basic Build Tooling

Goal: make local build/test commands predictable.

Inputs:

- Go module from `00.01`.

Implementation Notes:

- Add `Makefile` or equivalent task runner.
- Include:
  - `make fmt`
  - `make test`
  - `make build`
  - `make lint` if a linter is introduced.
- Build binaries into `bin/`.

Acceptance Criteria:

- `make fmt` formats code.
- `make test` runs tests.
- `make build` builds all three binaries.
- Build output is ignored by git when git is initialized.

Dependencies:

- `00.01`

Out Of Scope:

- Release packaging.

## 00.03 Add Version Command

Goal: every binary should report its version.

Inputs:

- Binary skeletons.

Implementation Notes:

- Add `version` command or `--version`.
- Use default development values:
  - version
  - commit
  - build date
- Allow values to be overridden via linker flags later.

Acceptance Criteria:

- `deployer version` prints version metadata.
- `deployer-server version` prints version metadata.
- `deployer-agent version` prints version metadata.

Dependencies:

- `00.01`

Out Of Scope:

- Automated release versioning.

## 00.04 Add Structured Logging Package

Goal: establish consistent logs for server and agent.

Inputs:

- Go module skeleton.

Implementation Notes:

- Use structured logs.
- Include log levels.
- Include component name.
- Avoid logging secrets by convention and helper functions.

Acceptance Criteria:

- Server startup log includes component and version.
- Agent startup log includes component and version.
- Tests cover redaction helper behavior if implemented.

Dependencies:

- `00.01`

Out Of Scope:

- Centralized log collection.

## 00.05 Add Configuration Loader

Goal: provide environment/file/flag based configuration for binaries.

Inputs:

- Server and agent skeletons.

Implementation Notes:

- Support env vars first.
- Optional config file support can be added if easy.
- Define server config:
  - listen address
  - database path/URL
  - public base URL
  - secret encryption key path/value
- Define agent config:
  - server URL
  - node credential path
  - WireGuard interface name

Acceptance Criteria:

- Server validates required config.
- Agent validates required config.
- Invalid config returns clear errors.

Dependencies:

- `00.01`

Out Of Scope:

- Dynamic config reload.

## 00.06 Add gRPC Error Convention

Goal: define common gRPC error behavior.

Inputs:

- Planned gRPC API.

Implementation Notes:

- Use canonical gRPC status codes.
- Include operator-friendly messages.
- Use structured error details later if needed.
- Do not leak internal errors directly.

Acceptance Criteria:

- Shared error helpers exist.
- Server can return a validation error with `InvalidArgument`.
- Not found maps to `NotFound`.
- Permission failures map to `Unauthenticated` or `PermissionDenied`.

Dependencies:

- `00.01`

Out Of Scope:

- Full API implementation.

## 00.07 Add Repository Documentation Stub

Goal: create a practical starting README.

Inputs:

- Main spec and implementation plan.

Implementation Notes:

- Add `README.md`.
- Explain project purpose.
- Link to `PRODUCT_SPEC.md`.
- Link to `IMPLEMENTATION_PLAN.md`.
- Add local development commands once available.

Acceptance Criteria:

- README exists.
- README points readers to the product spec and implementation plan.

Dependencies: none.

Out Of Scope:

- Full user documentation.

## 00.08 Add Token Generation And Hashing Helpers

Goal: provide one shared implementation for secure platform tokens.

Inputs:

- Auth model from the main spec.
- Control plane auth requirements.

Implementation Notes:

- Place helpers in `internal/security`.
- Generate tokens with Go `crypto/rand`, never `math/rand`.
- Use at least 32 random bytes per token.
- Encode tokens with URL-safe base64.
- Prefix tokens by type for operator clarity:
  - `dep_admin_`
  - `dep_join_`
  - `dep_agent_`
- Prefixes are not security boundaries; they are for readability and routing validation.
- Store hashes, not plaintext tokens.
- Prefer HMAC-SHA256 with a server-side token hashing key:
  - `HMAC_SHA256(token_hashing_key, raw_token)`
- Provide constant-time comparison for token hashes.
- Provide redaction helpers for logs and errors.

Acceptance Criteria:

- Token generation produces prefixed, URL-safe tokens.
- Generated tokens have at least 256 bits of entropy.
- Hashing the same token with the same key is stable.
- Different tokens produce different hashes.
- Redaction helper never returns the full token.
- Unit tests cover token generation, hashing, and redaction.

Dependencies:

- `00.01`

Out Of Scope:

- JWTs.
- OIDC tokens.
- mTLS certificates.
