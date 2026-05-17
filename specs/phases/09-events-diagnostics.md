# Phase 08.5: Events And Diagnostics

Goal: add a platform event log and diagnostic commands so operators can understand what changed, when it changed, and why the system is unhealthy.

Events are domain-level records. They are not raw application logs or debug logs. They should capture important state transitions and operator actions.

Examples:

```text
node.offline
node.online
app.deploy.started
app.deploy.failed
route.degraded
secret.updated
agent.update.failed
```

## EV.01 Add Event Data Model

Goal: define the event record shape.

Inputs:

- Data model draft.
- Platform debugging requirements.

Implementation Notes:

- Fields:
  - `id`
  - `created_at`
  - `type`
  - `severity`
  - `message`
  - optional `app_id`
  - optional `node_id`
  - optional `deployment_id`
  - optional `metadata_json`
- Severity values:
  - `info`
  - `warning`
  - `error`
- Event types should use dot-separated names, e.g. `node.offline`.

Acceptance Criteria:

- Event model is defined in code.
- Event type and severity constants exist for MVP events.
- Metadata supports structured details without schema changes.

Dependencies:

- `01.02`

Out Of Scope:

- Audit-grade tamper-proof logging.

## EV.02 Add Event Store Methods

Goal: persist and query platform events.

Inputs:

- SQLite store.
- Event data model.

Implementation Notes:

- Add `events` table.
- Support:
  - create event
  - list latest events
  - filter by app
  - filter by node
  - filter by type
  - filter by severity
  - filter by `since`
- Order newest-first by default.

Acceptance Criteria:

- Store can create and list events.
- Filters work independently and together.
- Tests cover ordering and filtering.

Dependencies:

- `01.03`
- `EV.01`

Out Of Scope:

- Full-text search.

## EV.03 Add Event Recorder Service

Goal: provide a single safe way for platform code to emit events.

Inputs:

- Event store.

Implementation Notes:

- Define interface:
  - `Record(ctx, Event) error`
- Recorder should fill:
  - ID
  - timestamp
- Do not fail primary operations solely because event recording fails unless the event is security-critical.
- Log event recording failures.

Acceptance Criteria:

- Code can record an event with minimal boilerplate.
- Recorder validates required fields.
- Recorder does not log secret metadata.

Dependencies:

- `EV.02`

Out Of Scope:

- Event bus or external sinks.

## EV.04 Add Event gRPC Service

Goal: expose events to CLI and future automation.

Inputs:

- Protobuf API contracts.
- Event store.

Implementation Notes:

- Add service:
  - `EventService`
- RPCs:
  - `ListEvents`
  - `WatchEvents`
- `WatchEvents` can poll internally for MVP if streaming from DB is not available.
- Support filters:
  - app
  - node
  - type
  - severity
  - since

Acceptance Criteria:

- CLI can list events through gRPC.
- Watch RPC streams new events.
- Auth is required.

Dependencies:

- `01.06`
- `EV.02`

Out Of Scope:

- Public unauthenticated event feeds.

## EV.05 Add CLI Events Commands

Goal: let operators inspect event history.

Inputs:

- Event gRPC service.
- CLI rendering helpers.

Implementation Notes:

- Commands:
  - `deployer events`
  - `deployer events --app <app>`
  - `deployer events --node <node>`
  - `deployer events --type <type>`
  - `deployer events --severity <severity>`
  - `deployer events --since <duration>`
  - `deployer events --watch`
- Human output columns:
  - time
  - severity
  - type
  - message

Acceptance Criteria:

- Events list in human output.
- JSON output works.
- Watch mode prints new events until interrupted.

Dependencies:

- `EV.04`
- `02.06`

Out Of Scope:

- Interactive TUI.

## EV.06 Emit Node Lifecycle Events

Goal: record meaningful node state transitions.

Inputs:

- Event recorder.
- Node join and heartbeat logic.

Implementation Notes:

- Emit:
  - `node.joined`
  - `node.online`
  - `node.offline`
  - `node.removed`
- Emit on transitions only.
- Avoid repeated `node.offline` events every detector loop.

Acceptance Criteria:

- Node join records an event.
- Online to offline records one event.
- Offline to online records one event.
- Events include node ID/name metadata.

Dependencies:

- `03.03`
- `03.05`
- `EV.03`

Out Of Scope:

- Alert notifications.

## EV.07 Emit App Deployment Events

Goal: record app rollout outcomes.

Inputs:

- Event recorder.
- App desired state.
- Kubernetes apply logic.

Implementation Notes:

- Emit:
  - `app.deploy.started`
  - `app.deploy.succeeded`
  - `app.deploy.failed`
- Include app, image, deployment ID, and failure reason metadata.
- Do not include secret values.

Acceptance Criteria:

- Deploy start records event.
- Successful apply records event.
- Failed apply records event with useful reason.

Dependencies:

- `04.07`
- `05.06`
- `EV.03`

Out Of Scope:

- Per-pod event mirroring.

## EV.08 Emit Health And Route Events

Goal: record degraded/recovered app and route states.

Inputs:

- Rollout status reader.
- Route status.
- Event recorder.

Implementation Notes:

- Emit:
  - `app.health.degraded`
  - `app.health.recovered`
  - `route.degraded`
  - `route.recovered`
- Emit on state transitions only.
- Include available/desired replica counts.

Acceptance Criteria:

- App health degradation records one event.
- App recovery records one event.
- Route degradation/recovery records events.

Dependencies:

- `05.07`
- `07.06`
- `EV.03`

Out Of Scope:

- External alert delivery.

## EV.09 Emit Secret Change Events

Goal: record secret management actions without leaking values.

Inputs:

- Secret service.
- Event recorder.

Implementation Notes:

- Emit:
  - `secret.created`
  - `secret.updated`
  - `secret.deleted`
- Metadata may include app and secret name.
- Metadata must never include secret value.

Acceptance Criteria:

- Secret create/update/delete records events.
- Event output shows secret name only.
- Tests ensure secret value is not present in event metadata.

Dependencies:

- `08.04`
- `EV.03`

Out Of Scope:

- Full security audit log.

## EV.10 Add Event Retention Policy

Goal: prevent unbounded event table growth.

Inputs:

- Event store.

Implementation Notes:

- Configurable retention:
  - max age, e.g. 30 days
  - or max count, e.g. 10,000 events
- Background cleanup loop.
- Default should be conservative.

Acceptance Criteria:

- Old events can be pruned.
- Cleanup behavior is configurable.
- Cleanup does not delete recent events.

Dependencies:

- `EV.02`

Out Of Scope:

- Archival export.

## EV.11 Extend Doctor With Event Context

Goal: make `deployer doctor` more useful by showing recent relevant events.

Inputs:

- Doctor command.
- Event service.

Implementation Notes:

- For `deployer doctor <app>`, show recent app events.
- For `deployer nodes inspect <node>`, optionally show recent node events.
- Keep output concise.

Acceptance Criteria:

- Doctor includes recent failed/degraded events.
- Output remains readable.

Dependencies:

- `10.04`
- `EV.05`

Out Of Scope:

- AI-generated diagnostics.

