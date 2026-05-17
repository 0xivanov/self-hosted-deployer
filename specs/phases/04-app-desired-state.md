# Phase 04: App Desired State

Goal: let operators define apps and persist desired deployment state before touching Kubernetes.

## 04.01 Define App Config Schema

Goal: formalize `deployer.yaml`.

Inputs:

- Main spec app config example.

Implementation Notes:

- Fields:
  - `name`
  - `image`
  - `service.port`
  - `service.health.path`
  - `routing.domain`
  - `deploy.replicas`
  - `deploy.strategy`
  - `placement.arch`
  - `placement.spread`
  - `placement.prefer`
  - `placement.fallback`
  - `secrets`
  - `state.mode`
- Start with YAML parsing.

Acceptance Criteria:

- Valid config parses.
- Missing required fields produce clear errors.
- Unknown fields are rejected or warned consistently.

Dependencies:

- `00.01`

Out Of Scope:

- JSON schema publishing.

## 04.02 Add Config Validation

Goal: catch bad app definitions before server submission.

Inputs:

- App config schema.

Implementation Notes:

- Validate:
  - app name DNS-safe enough for Kubernetes
  - image non-empty
  - port range
  - replicas >= 1
  - ARM64 placement default
  - valid state mode
  - health path starts with `/`

Acceptance Criteria:

- Validation tests cover valid and invalid configs.
- Error messages identify exact field.

Dependencies:

- `04.01`

Out Of Scope:

- Live registry image checks.

## 04.03 Add App Repository Methods

Goal: persist desired app state.

Inputs:

- SQLite repository.
- App schema.

Implementation Notes:

- Store normalized app fields.
- Store placement policy as JSON if needed.
- Store secret names, not values.

Acceptance Criteria:

- Repository can create/update/get/list apps.
- Updating app records `updated_at`.
- Tests cover duplicate app name behavior.

Dependencies:

- `01.03`
- `04.01`

Out Of Scope:

- Deployment history.

## 04.04 Add App gRPC Service Methods

Goal: expose desired app state management.

Inputs:

- App repository methods.
- Admin auth.

Implementation Notes:

- RPCs:
  - `AppService.DeployApp`
  - `AppService.ListApps`
  - `AppService.InspectApp`
  - `AppService.DeleteApp`
- Deleting an app should mark desired state deleted; actual cleanup can be later.

Acceptance Criteria:

- Create app succeeds.
- Update app changes desired state.
- List returns apps.
- Delete marks/removes app.
- Auth required.

Dependencies:

- `04.03`
- `01.04`

Out Of Scope:

- Kubernetes apply.

## 04.05 Add CLI Deploy Config Command

Goal: submit `deployer.yaml` to the control plane.

Inputs:

- App config parser.
- App API.

Implementation Notes:

- Command:
  - `deployer deploy`
- Default file:
  - `./deployer.yaml`
- Optional:
  - `--file`
  - `-f`
  - `--dry-run`
- `--file` / `-f` accepts a relative or absolute filesystem path.
- Examples:
  - `deployer deploy`
  - `deployer deploy --file ./deploy/deployer.yaml`
  - `deployer deploy -f /opt/apps/my-api/deployer.yaml`
- Dry run validates and prints normalized desired state.
- Dry run must not call the control plane.

Acceptance Criteria:

- `deployer deploy` reads `./deployer.yaml` by default.
- `deployer deploy --file <path>` reads the provided path.
- `deployer deploy -f <path>` reads the provided path.
- Dry run validates without server call.
- Deploy creates or updates app desired state.
- Human output shows app name, image, replicas, domain.

Dependencies:

- `04.02`
- `04.04`
- `02.04`

Out Of Scope:

- Rollout streaming.

## 04.06 Add App List/Inspect CLI Commands

Goal: let operators inspect desired app state.

Inputs:

- App API.

Implementation Notes:

- Commands:
  - `deployer apps list`
  - `deployer apps inspect <name>`
- Show desired replicas, image, state mode, domain.

Acceptance Criteria:

- List displays apps in table form.
- Inspect displays full config without secret values.
- JSON output works.

Dependencies:

- `04.04`
- `02.06`

Out Of Scope:

- Live pod status.

## 04.07 Add Deployment Record Repository

Goal: track deploy attempts separately from app desired state.

Inputs:

- App repository.

Implementation Notes:

- Create deployment record on app desired state change.
- Status values:
  - pending
  - applying
  - healthy
  - failed
- Store failure reason.

Acceptance Criteria:

- New deploy creates pending deployment.
- Deployment status can be updated.
- Tests cover deployment listing by app.

Dependencies:

- `04.03`

Out Of Scope:

- Kubernetes status integration.
