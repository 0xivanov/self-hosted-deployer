# Disposable hosted NetworkPolicy qualification

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
