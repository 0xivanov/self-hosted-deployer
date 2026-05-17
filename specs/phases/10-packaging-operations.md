# Phase 10: Packaging And Operations

Goal: make the platform installable and operable on a VPS plus ARM64 Linux nodes.

## 10.01 Add Systemd Unit Templates

Goal: run server and agent as services.

Inputs:

- Server binary.
- Agent binary.

Implementation Notes:

- Templates:
  - `deployer-server.service`
  - `deployer-agent.service`
- Include restart policy.
- Load environment file from `/etc/deployer/`.

Acceptance Criteria:

- Unit templates are valid.
- Documentation shows install path and env file.

Dependencies:

- `01.01`
- `03.07`

Out Of Scope:

- Automated installer.

## 10.02 Add Agent Installer Script

Goal: produce the one-line node join UX.

Inputs:

- Agent join command.
- Agent run loop.
- WireGuard setup.

Implementation Notes:

- Script accepts:
  - server URL
  - join token
- Detect OS/arch.
- Install or place agent binary.
- Run join.
- Create config.
- Install systemd unit.
- Start service.

Acceptance Criteria:

- Fresh node can join with one command in a supported environment.
- Script fails with clear message when prerequisites are missing.
- Token is not written to logs.

Dependencies:

- `03.06`
- `03.07`
- `06.06`
- `10.01`

Out Of Scope:

- Supporting every Linux distribution.

## 10.03 Add Server Bootstrap Command

Goal: simplify VPS setup for the control plane.

Inputs:

- Server binary.
- SQLite store.
- k3s expected install.
- WireGuard hub.

Implementation Notes:

- Command:
  - `deployer-server bootstrap`
- Validate prerequisites.
- Generate initial config.
- Generate admin token.
- Initialize DB.
- Optionally print next steps for k3s/WireGuard if not automating yet.

Acceptance Criteria:

- Bootstrap produces a runnable server config.
- Admin token is printed once and not stored plaintext if avoidable.
- Re-running detects existing config.

Dependencies:

- `01.03`
- `01.04`

Out Of Scope:

- Full OS provisioning.

## 10.04 Add Doctor Command

Goal: diagnose common platform setup issues.

Inputs:

- CLI.
- Server status.
- Node status.
- Kubernetes readiness.
- WireGuard status.

Implementation Notes:

- Command:
  - `deployer doctor`
- Check:
  - control plane reachable
  - auth valid
  - database ready
  - Kubernetes ready
  - WireGuard hub configured
  - ingress configured

Acceptance Criteria:

- Doctor reports pass/fail/warn.
- Failures include next-step guidance.

Dependencies:

- `02.05`
- `05.01`
- `06.07`
- `07.05`

Out Of Scope:

- Auto-fix mode.

## 10.05 Add ARM64 Build Target

Goal: produce binaries for Raspberry Pi/ARM64 Linux.

Inputs:

- Go build tooling.

Implementation Notes:

- Apple Silicon Macs and Raspberry Pis are both ARM64, but they need different binaries:
  - Apple Silicon Mac: `darwin/arm64`
  - Raspberry Pi with 64-bit Linux: `linux/arm64`
- Add build targets:
  - `deployer` for `darwin/arm64`
  - `deployer` for `linux/arm64`
  - `deployer-server` for `linux/arm64`
  - `deployer-agent` for `linux/arm64`
  - optional `deployer-server` for `linux/amd64`
- Document target matrix.

Acceptance Criteria:

- `make build-arm64` or equivalent works.
- Artifact names include OS/arch.

Dependencies:

- `00.02`

Out Of Scope:

- Signed releases.

## 10.06 Add Basic Release Packaging

Goal: package binaries and service templates for manual install.

Inputs:

- Build targets.
- systemd templates.

Implementation Notes:

- Create tarballs per OS/arch.
- Include:
  - binary
  - example config
  - systemd unit where relevant
  - checksum file

Acceptance Criteria:

- Release archive can be unpacked manually.
- Checksums are generated.

Dependencies:

- `10.01`
- `10.05`

Out Of Scope:

- Homebrew/Apt repositories.

## 10.07 Add End-To-End MVP Smoke Test Script

Goal: document and partially automate the MVP success path.

Inputs:

- CLI.
- Server.
- Agent.
- Kubernetes integration.

Implementation Notes:

- Local-kind/k3d-based smoke test may be acceptable for CI.
- Real VPS/ARM smoke test can be documented manually.
- Test deploys a tiny HTTP image.

Acceptance Criteria:

- Smoke test creates app, deploys, checks status.
- Manual VPS test checklist exists.

Dependencies:

- `05.06`
- `07.03`
- `08.07`

Out Of Scope:

- Full multi-node hardware lab automation.
