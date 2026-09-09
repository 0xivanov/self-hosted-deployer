# Disposable hosted NetworkPolicy qualification

## Two-node Mac rehearsal, September 9

`tests/mac-network-isolation-smoke.py` passed in the recovered Mac control plane with its existing worker, both running k3s v1.35.5+k3s1. The test copies the actual deployed hosting app's NetworkPolicy spec into a unique disposable namespace. It places a protected nonroot HTTP fixture on the worker and an unrelated fixture on the control plane. It does not remove or modify the existing app's policy.

All four paths passed baseline and recovery controls: unrelated-to-protected ingress and protected-to-unrelated egress, each through both Pod and Service IP addresses. With the policy applied, each path failed with a recognized network denial. Cluster DNS still resolved, and actual Traefik ingress reached the protected worker app through its Service. The original hosted app continued serving with unchanged Pod identities and restart counts.

Run only inside the prepared replacement lab, with the synthetic app image already available on both nodes:

```sh
limactl shell deployer-poc-restore -- sudo python3 - --apply < tests/mac-network-isolation-smoke.py
```

The helper requires root and the exact replacement guest hostname. It exclusively creates random namespaces and removes only those namespaces on exit. Both replacement and worker must be running; keep the original control-plane VM stopped. This is an explicit disposable qualification step, not a CI test against an arbitrary cluster.

This adds local multi-node Service routing and real ingress evidence. It does not prove isolation between independent customer VMs/control planes, resistance to privileged workloads, production provider networking, public TLS, monitoring delivery or resource-exhaustion containment. Separate customer VMs/clusters remain the primary customer boundary.

## Earlier single-node rehearsal, September 6

On 2026-09-06, `sh tests/hosting-cluster-smoke.sh` passed against pinned image `rancher/k3s@sha256:2074403abe1bded11ef3dde09d457e13be8e0b64c218b1c4f8269b4565cfbc65` (k3s v1.35.5+k3s1, matching the inventoried live version).

The reproducible test generates its NetworkPolicy using the actual Go renderer in an isolated source copy. It starts a uniquely named disposable privileged Docker container with no host filesystem mount or host kubeconfig. Cleanup removes only that container and its temporary source directory. Existing Docker workloads are untouched. Requirements are Docker, Go, Git, and access to the pinned k3s and BusyBox images; run it locally with `sh tests/hosting-cluster-smoke.sh`.

The fixture serves a known HTTP 200 body on two ports. Before policy application, both listeners are reachable. With the rendered policy applied:

- A Traefik-labeled probe in kube-system reaches the declared application port.
- A monitoring namespace probe reaches the declared application port.
- Unrelated namespace probes cannot reach either application port.
- The Traefik probe cannot reach the undeclared second port.
- The hosted Pod resolves the Kubernetes service through cluster DNS.
- The hosted Pod reaches a declared destination CIDR on the allowed egress port, while the second listening port is blocked.

After removing the policy, all previously blocked paths return the expected HTTP 200 body. Denials must be recognized network failures, not DNS, kubectl, or HTTP errors. This k3s installation actively rejects some forbidden connections with `Connection refused`; packet timeout is not the only valid enforcement result. Baseline and recovery checks establish that the destination listeners exist and work.

This replaces the earlier exploratory smoke result with reproducible evidence from the actual renderer and positive controls. Direct Pod IPs are used for traffic assertions. It does not qualify Service NAT behavior, live multi-node routing, actual Traefik or monitoring integration, namespace quotas under exhaustion, non-root image compatibility, managed PostgreSQL, or isolation between customer control planes. Those remain separate qualification gates. The single-node local result must not be presented as live customer isolation certification.
