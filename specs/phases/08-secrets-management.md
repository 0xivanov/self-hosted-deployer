# Phase 08: Secrets Management

Goal: safely store app secrets and distribute them only where needed.

## 08.01 Add Secret Encryption Key Config

Goal: configure the key used for MVP secret encryption.

Inputs:

- Server config.

Implementation Notes:

- Accept key from env var or file.
- Validate required length.
- Refuse to start secret APIs without key.

Acceptance Criteria:

- Missing key gives clear startup/config error.
- Invalid key length is rejected.
- Key value is never logged.

Dependencies:

- `00.05`

Out Of Scope:

- KMS integration.

## 08.02 Add Encryption Helper

Goal: encrypt/decrypt secret values consistently.

Inputs:

- Secret key config.

Implementation Notes:

- Use authenticated encryption.
- Include nonce per encrypted value.
- Return encoded ciphertext suitable for DB storage.

Acceptance Criteria:

- Encrypt/decrypt round trip works.
- Same plaintext produces different ciphertext due to nonce.
- Tampered ciphertext fails.

Dependencies:

- `08.01`

Out Of Scope:

- Envelope encryption.

## 08.03 Add Secret Repository Methods

Goal: persist encrypted app secrets.

Inputs:

- SQLite repository.
- Encryption helper.

Implementation Notes:

- Store:
  - app ID/name
  - secret name
  - encrypted value
  - timestamps
- Upsert by app/name.

Acceptance Criteria:

- Secret can be set.
- Secret can be listed by name.
- Secret can be deleted.
- Plaintext is not stored in DB.

Dependencies:

- `08.02`
- `01.03`

Out Of Scope:

- Secret version history.

## 08.04 Add Secret gRPC Service Methods

Goal: let CLI manage app secrets.

Inputs:

- Secret repository.
- Admin auth.

Implementation Notes:

- RPCs:
  - `SecretService.SetSecret`
  - `SecretService.ListSecrets`
  - `SecretService.DeleteSecret`
- List RPC returns names only.
- Values are accepted in request body but never returned.

Acceptance Criteria:

- Set secret works.
- List returns names only.
- Delete works.
- API never returns plaintext secret.

Dependencies:

- `08.03`
- `01.04`

Out Of Scope:

- Agent secret fetch API.

## 08.05 Add CLI Secret Commands

Goal: provide secure operator UX for secrets.

Inputs:

- Secret API.

Implementation Notes:

- Commands:
  - `deployer secrets set <app> <name>`
  - `deployer secrets list <app>`
  - `deployer secrets remove <app> <name>`
- Prompt for secret value without echo.
- Support `--value` only if clearly documented as less safe.

Acceptance Criteria:

- Set prompts securely.
- List does not show values.
- Remove asks for confirmation unless `--yes`.

Dependencies:

- `08.04`
- `02.04`

Out Of Scope:

- Secret import from `.env`.

## 08.06 Generate Kubernetes Secret Manifest

Goal: make app secrets available to pods.

Inputs:

- Secret repository.
- App config secret names.

Implementation Notes:

- Generate one Kubernetes Secret per app.
- Include only secrets referenced by that app.
- Mount as environment variables for MVP.

Acceptance Criteria:

- Deployment references Kubernetes Secret env vars.
- Missing required secret blocks deployment with clear error.
- Secret manifest tests cover key names.

Dependencies:

- `05.03`
- `08.03`

Out Of Scope:

- Secret file mounts.

## 08.07 Apply Kubernetes Secrets

Goal: create/update Kubernetes Secrets during deploy.

Inputs:

- Secret manifest generation.
- Kubernetes apply.

Implementation Notes:

- Apply Secret before Deployment.
- Trigger Deployment rollout when secret changes if needed.

Acceptance Criteria:

- App pod receives configured secret env var.
- Updating secret can restart or roll pods according to documented behavior.

Dependencies:

- `08.06`
- `05.05`

Out Of Scope:

- Secret rotation workflow.
