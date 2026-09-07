# Opt-in hosting profile

`hosting.version: v1` is an explicit per-application opt-in. Existing configurations omit `hosting` and retain their current Kubernetes rendering. The profile is stored with desired state, so an older client cannot remove it accidentally: updating an opted-in app without a hosting block is rejected before an app update, deployment record, secret resolution, route change, or runtime reconcile.

The profile requires CPU, memory, and ephemeral-storage requests and limits, plus `maxReplicas`. Requests and limits must parse as Kubernetes quantities, limits must be at least requests, and the effective replica count must fit the cap. Opted-in Pods set non-root execution, disable privilege escalation, drop all Linux capabilities, use the runtime-default seccomp profile, disable automatic service-account token mounting, and receive the declared resource requirements.

The implementation performs a conservative per-node capacity preflight, including visible nonterminal Pods, system and monitoring Pods, resource requests, resilient host spreading, and a rollout surge reservation. It does not claim equivalence with all Kubernetes scheduler behavior, and it fails closed when node or Pod capacity data is unknown. NetworkPolicy egress is explicit CIDR plus TCP port configuration, with DNS, Traefik ingress, and monitoring paths rendered by default. The installed CNI must still be tested for actual enforcement. Namespace ResourceQuota, LimitRange, and a stronger shared-cluster tenancy boundary remain separate requirements.

Hosted managed PostgreSQL is rejected until its operator, failover, and application traffic paths are separately qualified. External databases must be represented by explicit operator-approved egress CIDRs and ports.

There is no automatic adoption of legacy applications, namespaces, or workloads. A real Kubernetes runtime is required for an opted-in reconcile; a runtime without the node and Deployment clients fails closed before resource creation.

Deletion retains the NetworkPolicy while matching Pods still exist, including terminating Pods. After the Pods disappear, an operator may remove the owned leftover policy during offboarding. This avoids exposing a terminating workload when Kubernetes deletion is asynchronous.

Do not downgrade a server with opted-in applications to v0.3.0: that version drops unknown hosting fields when reading stored JSON. Legacy deployments without a hosting profile remain compatible with v0.3.0. Capacity admission is a conservative snapshot, not a scheduler reservation or an environment quota; simultaneous changes still require operator coordination.

## Read-only server preflight

```sh
deployer --context customer-a preflight --file deployer.yaml
deployer --context customer-a --output json preflight --file deployer.yaml
```

The new RPC checks configuration, hosting runtime support and capacity, profile removal, and domain ownership, returning normalized desired state and outstanding qualification warnings. It creates no application, deployment, route, event, secret, or Kubernetes resource. Capacity can change after the report. Deploy repeats its checks before mutation. An older server returns an unsupported-method error; there is no fallback to deployment. `deploy --dry-run` remains the existing local-only configuration preview.

Use [create-hosting-namespace.sh](../scripts/create-hosting-namespace.sh) for an explicit new-namespace quota and LimitRange preview. Applying it requires both a kubeconfig and a context and refuses an existing namespace. This helper does not provision a customer control plane or migrate applications.
