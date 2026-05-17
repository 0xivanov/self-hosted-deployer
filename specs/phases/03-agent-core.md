# Phase 03: Agent Core

Goal: implement node enrollment, identity, and heartbeat reporting.

## 03.01 Add Node Model Repository Methods

Goal: persist nodes and their lifecycle state.

Inputs:

- SQLite repository.
- Node data model.

Implementation Notes:

- Implement create, get, list, update status, update last seen.
- Support labels:
  - location
  - arch
  - role
- Start with JSON labels if relational labels are too much for MVP.

Acceptance Criteria:

- Repository tests cover create/list/get/update.
- Node status defaults to pending or online depending creation path.

Dependencies:

- `01.03`

Out Of Scope:

- Kubernetes node synchronization.

## 03.02 Add Join Token Repository Methods

Goal: create and consume one-time node join tokens.

Inputs:

- SQLite repository.

Implementation Notes:

- Store token hashes only.
- Raw join token is displayed once by the CLI as part of the install/join command.
- Tokens expire.
- Tokens can be consumed once.
- Associate intended node name and labels.

Acceptance Criteria:

- Token can be created.
- Valid token can be consumed once.
- Expired token is rejected.
- Used token is rejected.
- Raw token is never stored.
- Raw token is only returned at creation time.

Dependencies:

- `01.03`

Out Of Scope:

- Human approval queue.

## 03.03 Add Node Join RPC

Goal: allow a new agent to exchange a join token for node identity.

Inputs:

- Node repository.
- Join token repository.

Implementation Notes:

- RPC:
  - `NodeService.JoinNode`
- Request:
  - join token
  - hostname
  - arch
  - public key material placeholder
- Response:
  - node ID
  - node name
  - generated agent token
- The server generates the agent token during successful join.
- The server stores only the agent token hash.
- The raw agent token is returned directly to the joining agent once.
- The CLI should not normally display the agent token.
- The agent stores the raw agent token locally with restrictive permissions.

Acceptance Criteria:

- Valid join token creates/activates node.
- Invalid token returns 401/403.
- Reused token fails.
- Agent credential is returned only once.
- Raw agent token is not logged.

Dependencies:

- `03.01`
- `03.02`
- `00.08`

Out Of Scope:

- mTLS certificates.

## 03.04 Add Agent Credential Authentication

Goal: authenticate agent RPCs separately from admin CLI RPCs.

Inputs:

- Agent credential from join API.

Implementation Notes:

- Agent uses gRPC metadata:
  - `authorization: Bearer <agent-token>`
- Server maps token to node.
- Agent RPCs are scoped to that node.

Acceptance Criteria:

- Agent RPC rejects admin token if not intended.
- Agent RPC rejects invalid token.
- Valid agent token identifies exactly one node.

Dependencies:

- `03.03`

Out Of Scope:

- Certificate rotation.

## 03.05 Add Heartbeat RPC

Goal: let nodes report liveness and basic metadata.

Inputs:

- Agent auth.

Implementation Notes:

- RPC:
  - `NodeService.Heartbeat`
- Request includes:
  - node status
  - hostname
  - arch
  - OS
  - kernel
  - optional resource summary
- Server updates `last_seen_at`.

Acceptance Criteria:

- Valid heartbeat updates node last seen.
- Node becomes online after heartbeat.
- Heartbeat cannot update another node.

Dependencies:

- `03.04`

Out Of Scope:

- Metrics history.

## 03.06 Add Agent Join Command

Goal: implement the agent-side registration flow.

Inputs:

- Join API.
- Agent config.

Implementation Notes:

- Command:
  - `deployer-agent join --server <url> --token <join-token>`
- Detect hostname and arch.
- Store returned credentials on disk with restrictive permissions.

Acceptance Criteria:

- Agent can join a local control plane.
- Credentials are persisted.
- Re-running join with used token fails clearly.

Dependencies:

- `03.03`
- `00.05`

Out Of Scope:

- Installer script.

## 03.07 Add Agent Run Loop

Goal: keep an enrolled node connected by sending heartbeats.

Inputs:

- Agent credentials.
- Heartbeat API.

Implementation Notes:

- Command:
  - `deployer-agent run`
- Send heartbeat on startup and fixed interval.
- Use backoff on failure.
- Log connection state transitions.

Acceptance Criteria:

- Agent sends periodic heartbeats.
- Server node list reflects online status.
- Bad credentials stop with clear error.

Dependencies:

- `03.05`
- `03.06`

Out Of Scope:

- Workload reconciliation.

## 03.08 Add CLI Node Commands

Goal: let the operator create join tokens and inspect nodes.

Inputs:

- Node APIs.
- Join token API.

Implementation Notes:

- Commands:
  - `deployer nodes add <name>`
  - `deployer nodes list`
  - `deployer nodes inspect <name>`
- `nodes add` prints the install/join command.

Acceptance Criteria:

- Add command creates token.
- List shows node status.
- Inspect shows labels and last seen.
- JSON output works.

Dependencies:

- `03.01`
- `03.02`
- `02.04`

Out Of Scope:

- Drain/remove.
