# Phase 06: WireGuard Networking

Goal: automate private networking between the VPS and worker nodes.

## 06.01 Define WireGuard Address Allocation

Goal: assign stable private IPs to nodes.

Inputs:

- Node model.
- MVP subnet `10.8.0.0/24`.

Implementation Notes:

- Store WireGuard IP on node.
- Reserve `10.8.0.1` for VPS hub.
- Allocate sequential IPs for nodes.
- Avoid reusing removed node IPs initially unless explicitly reclaimed.

Acceptance Criteria:

- New node receives unique WireGuard IP.
- Collision is prevented.
- Tests cover allocation edge cases.

Dependencies:

- `03.01`

Out Of Scope:

- Multiple VPN subnets.

## 06.02 Capture Node WireGuard Public Key During Join

Goal: collect peer key material at enrollment.

Inputs:

- Agent join flow.

Implementation Notes:

- Agent generates WireGuard keypair locally.
- Agent sends public key during join.
- Private key never leaves node.
- Server stores public key.

Acceptance Criteria:

- Join request includes public key.
- Server stores public key.
- Private key is persisted only on node.

Dependencies:

- `03.03`
- `03.06`

Out Of Scope:

- Key rotation.

## 06.03 Generate Hub Peer Config

Goal: represent node peers in VPS WireGuard config.

Inputs:

- Node public key.
- Node WireGuard IP.

Implementation Notes:

- Generate peer blocks:
  - `PublicKey`
  - `AllowedIPs = <node-ip>/32`
- Keep generation deterministic.

Acceptance Criteria:

- Unit tests assert generated peer block.
- Disabled/removed nodes are excluded.

Dependencies:

- `06.01`
- `06.02`

Out Of Scope:

- Direct file writes.

## 06.04 Apply Hub WireGuard Config

Goal: update the VPS WireGuard interface when peers change.

Inputs:

- Generated peer config.

Implementation Notes:

- Prefer safe application through `wg set` or managed config plus controlled reload.
- Make behavior idempotent.
- Log changes without private keys.

Acceptance Criteria:

- Adding node adds peer to hub.
- Removing node removes peer from hub.
- Re-apply does not duplicate peers.

Dependencies:

- `06.03`

Out Of Scope:

- Firewall automation.

## 06.05 Generate Node WireGuard Config

Goal: give the agent enough config to connect to the hub.

Inputs:

- Node private key.
- Node WireGuard IP.
- VPS public endpoint.
- Hub public key.

Implementation Notes:

- Agent writes config:
  - interface private key
  - node VPN address
  - hub public key
  - endpoint
  - allowed IPs
  - persistent keepalive
- File permissions should be restrictive.

Acceptance Criteria:

- Agent can render config without exposing private key in logs.
- Config includes `PersistentKeepalive = 25`.

Dependencies:

- `06.02`

Out Of Scope:

- Starting the interface.

## 06.06 Bring Up Node WireGuard Interface

Goal: connect node to private network.

Inputs:

- Node WireGuard config.

Implementation Notes:

- Use systemd or `wg-quick` depending OS support.
- Detect missing WireGuard tools and produce install guidance.
- Make command idempotent.

Acceptance Criteria:

- Agent can bring interface up.
- Re-running does not fail if interface is already up.
- Failure returns actionable error.

Dependencies:

- `06.05`

Out Of Scope:

- Installing OS packages automatically.

## 06.07 Add Connectivity Check

Goal: verify node can reach the VPS over WireGuard.

Inputs:

- WireGuard interface.

Implementation Notes:

- Agent can ping or TCP-check the server VPN IP.
- Report VPN status in heartbeat.

Acceptance Criteria:

- Heartbeat includes VPN connected/disconnected.
- CLI node inspect shows VPN status.

Dependencies:

- `06.06`
- `03.05`

Out Of Scope:

- Full mesh connectivity checks.

