# Phase 11: Releases And Automated Updates

Goal: publish versioned binaries through GitHub Releases and provide a controlled update path for the VPS control plane and node agents.

This phase is post-MVP. The first MVP can be installed manually from local builds or release archives. This phase makes the platform feel operationally real: new versions are built automatically, uploaded to GitHub Releases, and rolled out safely.

## 11.01 Define Release Artifact Matrix

Goal: decide exactly which binaries and archives each release publishes.

Inputs:

- Go binaries:
  - `deployer`
  - `deployer-server`
  - `deployer-agent`
- ARM64-first architecture requirement.

Implementation Notes:

- Treat OS and architecture as separate release dimensions.
- Apple Silicon Macs and Raspberry Pis are both ARM64, but they do not use the same binary:
  - Mac: `darwin/arm64`
  - Raspberry Pi: `linux/arm64`

- Release at least:
  - `deployer_darwin_arm64.tar.gz`
  - `deployer_linux_arm64.tar.gz`
  - `deployer-server_linux_arm64.tar.gz`
  - `deployer-agent_linux_arm64.tar.gz`
- Consider optional AMD64 artifacts for control-plane convenience:
  - `deployer_linux_amd64.tar.gz`
  - `deployer-server_linux_amd64.tar.gz`
  - `deployer-agent_linux_amd64.tar.gz`
- Include:
  - binary
  - README or install notes
  - example config where useful
  - systemd unit where useful
- Generate checksums for every artifact.

Acceptance Criteria:

- Artifact names are documented.
- Target OS/arch matrix is documented.
- Each artifact has a matching checksum entry.

Dependencies:

- `10.05`
- `10.06`

Out Of Scope:

- Package repositories.

## 11.02 Add GitHub Actions Release Workflow

Goal: build and upload binaries automatically when a version tag is pushed.

Inputs:

- Build tooling.
- Release artifact matrix.

Implementation Notes:

- Trigger on tags such as `v0.1.0`.
- Run tests before building release artifacts.
- Build all configured OS/arch targets.
- Create GitHub Release.
- Upload artifacts and checksums.
- Keep workflow permissions minimal.

Acceptance Criteria:

- Pushing a version tag creates a GitHub Release.
- Release contains all expected artifacts.
- Release contains checksum file.
- Workflow fails if tests fail.

Dependencies:

- `11.01`

Out Of Scope:

- Signing artifacts.

## 11.03 Embed Version Metadata In Binaries

Goal: make each binary know its release version.

Inputs:

- Existing `version` command.
- GitHub Actions release workflow.

Implementation Notes:

- Set linker flags during release build:
  - version
  - commit SHA
  - build date
- Ensure all binaries expose version consistently.

Acceptance Criteria:

- Release binary prints the tag version.
- Release binary prints commit SHA.
- Development binary still works with default metadata.

Dependencies:

- `00.03`
- `11.02`

Out Of Scope:

- Runtime update checks.

## 11.04 Add Release Metadata Fetcher

Goal: let the platform discover the latest available GitHub Release.

Inputs:

- GitHub Releases URL.
- Current binary version.

Implementation Notes:

- Add shared package for fetching release metadata.
- Support configurable repository owner/name.
- Parse semantic versions.
- Ignore prereleases by default unless configured.
- Respect network timeouts.

Acceptance Criteria:

- Fetcher returns latest stable release.
- Fetcher can include prereleases when requested.
- Network failures return clear errors.

Dependencies:

- `11.03`

Out Of Scope:

- Downloading artifacts.

## 11.05 Add CLI Version Check Command

Goal: let the operator check whether CLI/server/agent updates are available.

Inputs:

- Release metadata fetcher.
- Server version/status RPC.
- Agent version reporting later in this phase.

Implementation Notes:

- Command:
  - `deployer update check`
- Show:
  - local CLI version
  - server version
  - latest release version
  - known agent versions if available
- Do not auto-update by default.

Acceptance Criteria:

- Command reports when local CLI is outdated.
- Command reports when server is outdated.
- Network failure is non-destructive and readable.

Dependencies:

- `11.04`
- `02.05`

Out Of Scope:

- Applying updates.

## 11.06 Add Agent Version Reporting

Goal: know which version each node agent is running.

Inputs:

- Agent heartbeat.
- Version metadata.

Implementation Notes:

- Add agent version fields to heartbeat:
  - version
  - commit
  - build date
- Store latest reported version on node.
- Show version in node inspect/list where useful.

Acceptance Criteria:

- Node heartbeat records agent version.
- CLI can display agent version per node.
- Missing version from old agents is handled.

Dependencies:

- `03.05`
- `11.03`

Out Of Scope:

- Updating agents.

## 11.07 Add Artifact Downloader And Verifier

Goal: safely download release artifacts before installing them.

Inputs:

- Release metadata.
- Checksums.

Implementation Notes:

- Download artifact for current OS/arch.
- Download checksum file.
- Verify checksum before use.
- Store downloaded artifact in a staging directory.
- Never execute or install an unverified artifact.

Acceptance Criteria:

- Valid artifact downloads and verifies.
- Checksum mismatch aborts.
- Unsupported OS/arch gives clear error.

Dependencies:

- `11.04`

Out Of Scope:

- Signature verification.

## 11.08 Add VPS Server Self-Update Command

Goal: update the control plane binary on the VPS in a controlled way.

Inputs:

- Artifact downloader.
- systemd service template.

Implementation Notes:

- Command:
  - `deployer-server update`
- Intended to run on the VPS.
- Steps:
  - check latest release
  - download matching `deployer-server` artifact
  - verify checksum
  - stop service or prepare systemd restart
  - replace binary atomically
  - restart service
  - run post-update health check
- Keep previous binary for rollback.

Acceptance Criteria:

- Server can update to a newer release.
- Failed verification does not change binary.
- Failed restart rolls back or provides clear manual rollback instructions.
- Previous binary is retained.

Dependencies:

- `11.07`
- `10.01`

Out Of Scope:

- Remote SSH-based update from laptop.

## 11.09 Add CLI-Initiated Server Update

Goal: let the operator request a VPS update through the CLI.

Inputs:

- Server self-update command or API.
- Admin auth.

Implementation Notes:

- Command:
  - `deployer update server`
- Preferred flow:
  - CLI calls control plane admin RPC.
  - Control plane starts self-update job.
  - CLI streams job status or polls.
- Require explicit confirmation.
- Include `--target-version`.

Acceptance Criteria:

- CLI can trigger a server update.
- CLI shows progress and final status.
- Server refuses downgrade unless explicitly allowed.

Dependencies:

- `11.08`
- `02.04`

Out Of Scope:

- Updating the VPS OS packages.

## 11.10 Add Agent Update Job Model

Goal: represent desired agent version and rollout status.

Inputs:

- Node model.
- Agent version reporting.

Implementation Notes:

- Store update job:
  - target version
  - selected nodes
  - status
  - started/completed timestamps
  - per-node result
- Support rollout states:
  - pending
  - downloading
  - installing
  - restarted
  - healthy
  - failed

Acceptance Criteria:

- Update job can be created.
- Per-node status can be updated.
- CLI/API can inspect update job.

Dependencies:

- `11.06`

Out Of Scope:

- Performing update.

## 11.11 Add Agent Self-Update Command

Goal: let an agent update its own binary safely.

Inputs:

- Artifact downloader.
- systemd service template.

Implementation Notes:

- Command:
  - `deployer-agent update --target-version <version>`
- Steps:
  - download matching agent artifact
  - verify checksum
  - stage new binary
  - swap binary atomically
  - restart service
  - retain previous binary
- Agent should report update result after restart.

Acceptance Criteria:

- Agent can update itself on linux/arm64.
- Failed verification does not change binary.
- Previous binary is retained.
- Agent heartbeat resumes after update.

Dependencies:

- `11.07`
- `10.01`

Out Of Scope:

- Updating application workloads.

## 11.12 Add Control Plane Driven Agent Rollout

Goal: let the VPS coordinate agent updates across nodes.

Inputs:

- Agent update job model.
- Agent self-update command.
- Agent heartbeat/control loop.

Implementation Notes:

- Control plane creates update job.
- Agents poll for assigned update tasks.
- Each agent downloads and installs its own update.
- Roll out in small batches.
- Skip offline nodes until they return.
- Stop rollout if failure threshold is exceeded.

Acceptance Criteria:

- Control plane can update one selected agent.
- Control plane can update all online agents in batches.
- Offline agents remain pending.
- Failed agents are reported clearly.

Dependencies:

- `11.10`
- `11.11`
- `03.07`

Out Of Scope:

- Peer-to-peer artifact distribution.

## 11.13 Add CLI Agent Update Commands

Goal: provide operator-facing commands for agent rollouts.

Inputs:

- Control plane driven rollout.

Implementation Notes:

- Commands:
  - `deployer update agents`
  - `deployer update agents --nodes node-a,node-b`
  - `deployer update status <job-id>`
- Require confirmation for all-node rollout.
- Support `--target-version`.
- Support `--batch-size`.

Acceptance Criteria:

- CLI can start an agent update rollout.
- CLI can inspect update job status.
- CLI shows pending/offline/failed nodes.

Dependencies:

- `11.12`

Out Of Scope:

- Automatic background update without operator request.

## 11.14 Add Rollback Commands

Goal: recover from bad server or agent releases.

Inputs:

- Previous binary retention.
- Update job model.

Implementation Notes:

- Commands:
  - `deployer-server rollback`
  - `deployer-agent rollback`
  - `deployer update agents --rollback`
- Rollback should restore previous binary and restart service.
- For agents, rollback can be requested through the control plane.

Acceptance Criteria:

- Server rollback restores previous binary.
- Agent rollback restores previous binary.
- Rollback result is visible in status.

Dependencies:

- `11.08`
- `11.11`
- `11.13`

Out Of Scope:

- Database schema downgrade automation.

## 11.15 Add Update Safety Documentation

Goal: document the supported update process and risks.

Inputs:

- Release workflow.
- Server and agent update commands.

Implementation Notes:

- Document:
  - how releases are created
  - how to check versions
  - how to update the VPS
  - how to roll out agent updates
  - how to rollback
  - what happens to offline nodes
  - database migration caveats

Acceptance Criteria:

- Operator can follow docs to update from one version to another.
- Docs clearly warn that database downgrades are not automatically supported.

Dependencies:

- `11.09`
- `11.13`
- `11.14`

Out Of Scope:

- Fully automated unattended updates.
