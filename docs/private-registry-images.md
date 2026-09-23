# Private registry images

Implementation status: local source support, not installed on the live Launchstead fleet. The customer portal does not yet collect or attach private credentials. Real private-image pulls on the Pi workers remain unqualified.

## Credential model

The operator creates an immutable registry credential revision scoped to an application name. Creation is possible before the application's first deployment. Revisions are 64 lowercase hexadecimal characters chosen by the caller; retrying the same revision and login is idempotent. Different login details require a new revision. Each application may retain up to 100 revisions.

The dedicated `registry_credentials` table stores authenticated ciphertext using the server's existing secret encryption key. The encrypted payload binds the application, registry and revision, preventing a copied ciphertext from becoming another application's credential. Application environment secrets are stored and resolved separately. Listing returns only application name, revision, registry and creation time. There is no RPC to read login details or overwrite an existing revision.

The operator-only `RegistryCredentialService` exposes `CreateRegistryCredential` and `ListRegistryCredentials`. Authentication rejects agent tokens. The mutation audit records application, revision and registry metadata, never the username, token, request body or Docker configuration. The portal must enforce its project-to-application permission boundary before using these operator APIs.

## Operator command

Save a JSON file containing only `username` and `password`, owned by the current user with permissions `0600` or `0400`. The password is the registry access token. The CLI rejects symlinks, non-regular files, other permissions, unknown JSON fields and files over 16 KiB. Do not put the token in command-line arguments or commit the file.

```sh
deployer registry create --credentials /private/path/registry.json my-api <64-hex-revision> ghcr.io
deployer registry list my-api
```

Use the same revision when retrying a request whose result was lost. Generate a new random revision for rotation. Commands use the existing verified server connection and configured administrator authentication. Output contains only metadata.

## Deployment configuration

Add the opaque revision to the ordinary deployment configuration:

```yaml
name: my-api
image: ghcr.io/my-organization/my-api@sha256:<verified-64-character-digest>
imagePullCredential: <64-character-credential-revision>
```

The remainder of the ordinary deployment configuration is still required. Only fully qualified, digest-pinned Docker Hub or GHCR references are accepted with a private credential. The registry must match the saved credential. Preflight and deployment resolve the revision before application or deployment records are changed. A runtime without private pull support rejects the request.

The controller writes a separate immutable `kubernetes.io/dockerconfigjson` Secret, with a deterministic name derived from application and revision. It checks resource ownership, type, immutability, revision and content before reusing an existing Secret. Pods receive an `imagePullSecrets` reference, never the registry login through their environment. Kubernetes stores the pull configuration according to the cluster's Secret-storage configuration; the application's encrypted database alone does not provide Kubernetes encryption at rest.

## Rotation, rollback and deletion

Create a new revision and publish a release referencing it. Prior credential revisions and Kubernetes pull Secrets remain available. If applying an update fails, the deployer restores the previous desired configuration using its original revision. Switching to a public image removes the Pod reference but retains old credentials for retained releases. A successful final application deletion removes the application's owned pull Secrets and stored registry credentials. Cleanup refuses resources with conflicting ownership or identity.

There is no individual credential-deletion API yet because retained release references must be accounted for first. Credentials staged before an application ever exists can remain as orphan metadata; operator-safe orphan cleanup and reference-aware garbage collection remain follow-up work. Do not revoke a registry token at the provider while active or restorable releases still need it.

Credential persistence does not prove registry access or image safety. The platform must validate registry metadata, architecture, resource policy and pull behavior separately. The deployer's resource-apply response is not proof of completed rollout; customer publication still requires the fleet's runtime, replica and HTTPS checks.

## Compatibility and qualification

Goose migration 7 adds only the credential table. Existing application, environment-secret and deployment records are retained. Configurations without `imagePullCredential` retain the public-image path. Back up the control-plane database and encryption key before installing the new server. Do not roll back to an older server after using private references without first restoring compatible application configurations: older runtime code does not resolve this field.

Focused local checks cover encrypted storage, scope binding, immutable retries, capacity, migration preservation, operator authorization, metadata-only responses, pre-write rejection, previous-credential rollback, generated pull Secrets, ownership collisions and isolated deletion. These use temporary databases and fake Kubernetes clients. They do not prove live registry reachability, real kubelet pull behavior, provider permissions or full customer-portal integration.
