# Phase 02: CLI Core

Goal: build the operator CLI foundations in Go and connect it to the control plane.

## 02.01 Create CLI Root Command

Goal: establish the `deployer` command shape.

Inputs:

- Binary skeleton.

Implementation Notes:

- Implement this command in Go.
- Use a conventional Go CLI framework or standard flags.
- Add global flags:
  - `--server`
  - `--token`
  - `--config`
  - `--output`
- Support output formats:
  - human
  - json

Acceptance Criteria:

- `deployer --help` shows global flags.
- Unknown commands return clear errors.

Dependencies:

- `00.01`

Out Of Scope:

- Full command set.

## 02.02 Add CLI Config File

Goal: avoid passing server/token every time.

Inputs:

- CLI root command.

Implementation Notes:

- Store config under user config directory.
- Config fields:
  - current server URL
  - admin token or token reference
  - default output format
- Set restrictive file permissions where possible.

Acceptance Criteria:

- CLI reads config file.
- CLI flags override config.
- Missing config gives actionable message.

Dependencies:

- `02.01`

Out Of Scope:

- OS keychain integration.

## 02.03 Add Login Command

Goal: configure CLI access to the control plane.

Inputs:

- Static admin token auth.
- CLI config file.

Implementation Notes:

- Command:
  - `deployer login <server-url>`
- Prompt for admin token if not passed.
- Validate by calling a version/status RPC or a protected whoami-style RPC.

Acceptance Criteria:

- Valid token is saved.
- Invalid token is rejected.
- Server URL is normalized.

Dependencies:

- `01.04`
- `02.02`

Out Of Scope:

- Browser-based OAuth login.

## 02.04 Add gRPC API Client

Goal: centralize gRPC behavior for CLI commands.

Inputs:

- API DTOs.
- CLI config.

Implementation Notes:

- Add typed client methods.
- Attach bearer token as gRPC metadata.
- Decode gRPC status errors into readable CLI messages.
- Add request timeout.

Acceptance Criteria:

- Client handles successful unary RPCs.
- Client handles gRPC status errors with readable messages.
- Tests cover auth metadata and error decoding.

Dependencies:

- `01.06`
- `02.02`

Out Of Scope:

- Retry policy.

## 02.05 Add Status/Version Connectivity Command

Goal: verify CLI can talk to server.

Inputs:

- API client.

Implementation Notes:

- Add:
  - `deployer server status`
- Call PlatformService status/version RPC.
- Show server version and readiness.

Acceptance Criteria:

- Command prints server version.
- JSON output works.
- Bad server URL gives clear error.

Dependencies:

- `02.04`
- `01.01`

Out Of Scope:

- Platform-wide app status.

## 02.06 Add Output Rendering Helpers

Goal: keep human and JSON output consistent.

Inputs:

- CLI root output flag.

Implementation Notes:

- Add table rendering for human output.
- Add JSON output for automation.
- Avoid printing secrets through generic render helpers.

Acceptance Criteria:

- At least one command supports human and JSON output.
- Tests cover JSON output shape.

Dependencies:

- `02.01`

Out Of Scope:

- YAML output.
