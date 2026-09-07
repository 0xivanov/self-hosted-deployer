# Hosting resource and security validation

`tests/hosting-resources-smoke.sh` is a bounded local qualification test for the hosting profile. It starts one disposable privileged Docker container with the pinned k3s image used by the cluster smoke test, and removes the container and temporary images when it exits. It does not mount a host path, kubeconfig, Docker socket, SSH configuration, or live cluster context into k3s.

The test builds two short lived images from local BusyBox layers. The application image runs as numeric UID `65532`; the second image has the same contents but runs as UID `0`. Both images are imported into the k3s container's own containerd. The deployment YAML is emitted by `deploymentForApp` from an isolated copy of the Go sources, so the test observes the same resource and security rendering used by the controller.

Run it from the repository root with:

```sh
tests/hosting-resources-smoke.sh
```

Successful evidence includes a Ready k3s node, an Available nonroot application serving HTTP, admitted `runAsNonRoot=true` and `allowPrivilegeEscalation=false` settings with the configured requests and limits, a quota admission rejection for a second over-budget Pod while the healthy app remains Available, and kubelet rejection of the root based image.

The quota comes from `scripts/create-hosting-namespace.sh` in preview mode and is applied to a newly created namespace inside k3s. The test uses a small `100m` CPU, `64Mi` memory, `64Mi` ephemeral storage, and ten Pod budget to keep admission deterministic while isolating CPU quota rejection. It intentionally does not force memory or CPU exhaustion, kill Pods, or test host level cgroup behavior. Those behaviors depend on the Docker Desktop or host kernel runtime and are outside this local qualification's bounded evidence. This smoke test therefore does not claim the complete P3 acceptance surface by itself.

If Docker, the daemon, or the network needed to obtain the pinned k3s and BusyBox base images is unavailable, the test exits before creating a cluster. A successful run prints the pinned k3s digest and the quota and root image checks.
