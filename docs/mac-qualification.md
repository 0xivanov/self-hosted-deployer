# Mac Linux VM qualification

## Environment

On September 8, 2026, the base customer control-plane workflow was exercised on this Mac using Lima 2.2.0 and Apple's VZ backend. The checked-in definition is `deploy/qualification/mac-vm.yaml`: Ubuntu 24.04 ARM64, a pinned image digest, two CPUs, 3 GiB RAM and a 12 GiB sparse disk. No host directories or SSH agent are shared. TCP application and UDP port forwarding are disabled; Lima's localhost SSH transport remains available.

With Lima installed, create the disposable host from the repository root:

```sh
limactl start --name=deployer-poc-lab --tty=false deploy/qualification/mac-vm.yaml
limactl shell deployer-poc-lab
```

Resume or stop the existing host without deleting its disk:

```sh
limactl start --tty=false deployer-poc-lab
limactl stop deployer-poc-lab
```

Starting this VM only prepares Linux. To repeat provisioning, supply the manifest and prerequisites described in [customer provisioning](provisioning-customer-environment.md). An already provisioned host is deliberately refused by the installer.

## Executed rehearsal

The candidate archive was built from Go commit `e8a4334` as `v0.3.1-poc.20260908`, with real ARM64 binaries and an exact archive SHA-256. A guest-local HTTPS artifact server and synthetic CA supplied the unpublished archive through `release.base_url`. No release was published. The CA was trusted only inside the guest. k3s `v1.35.5+k3s1` and its installer were downloaded and checksum verified.

A private harness imported the provisioning module, validated the manifest, and called `run_provision(..., apply=True)`. Its Runner passed every generated command through `limactl shell ... sudo bash -lc`; only the SSHRunner transport was substituted. This does not exercise the outer CLI registry lock or normal SSH host-key setup.

The first run exposed a real startup race: k3s had started but its node did not yet exist when `kubectl wait` ran. The installer now polls for registration before waiting for Ready. The initial guest was explicitly repaired to finish the remaining stages, then stopped and restarted. All three services returned active, the node became Ready and the readiness endpoint returned `ready`.

The initial encrypted synthetic platform backup and its synthetic key were copied into protected Mac storage. Restoring with the Mac deployer-server binary recovered eleven SQLite tables and passed `PRAGMA integrity_check` outside the source guest.

The disposable guest was then deleted and recreated from the pinned image. The corrected workflow passed all twelve stages from a clean host without manual repair. Checks confirmed:

- `deployer-server`, `k3s` and `wg-quick@wg0` active.
- The control-plane node Ready and `/readyz` returning `ready`.
- Verified management TLS and an initial encrypted database backup.
- Namespace `hosting-mac-lab-a` with a quota of 1 CPU, 768 MiB memory, 1 GiB ephemeral storage and 20 pods.
- No virtiofs host mounts, with Lima reporting TCP application and UDP forwarding disabled.

The clean installation also passed a stop/start cycle: all three services were active, the node was Ready and `/readyz` returned `ready`. The VM is stopped after qualification to release RAM; its disk is retained for the next tests.

Nine provisioning tests and two generated-shell tests pass, including delayed node registration and checksum rejection before installation. Private manifests, bootstrap output, keys and diagnostic evidence remain outside Git under `~/.local/state/deployer/qualification/20260908-mac/`.

## Limits and next work

This qualifies base control-plane installation on an ARM64 Ubuntu VM and selected recovery behavior. It does not qualify a provider firewall, AMD64, public DNS or certificate renewal, worker onboarding, application ingress, cross-customer access, application-volume recovery, real alert delivery, a full host rebuild, or pilot duration gates. The synthetic database restore is not a full-environment restore.

No existing VPS or Pi deployment was changed by this rehearsal. Continue customer onboarding and recovery tests in the disposable lab before applying the new workflow to a customer environment.

## App lifecycle rehearsal, September 8

The next run found that node readiness had hidden failed Traefik and service-load-balancer containers. Their extracted overlay snapshots contained zero-length executable files, while the corresponding compressed image layers contained the expected data. The underlying corruption cause is not established. Switching this disposable guest to `snapshotter: native` in `/etc/rancher/k3s/config.yaml` and restarting k3s re-extracted working images. This is a lab workaround, not a fleet configuration change or evidence that overlay snapshots are generally unsafe.

The real CLI then logged into an identity-bound `mac-lab-a` context and deployed the nonroot ARM64 fixture in `deploy/qualification/app/`. Traffic through Traefik at `http://10.81.0.1/`, with the `Host: app.deployer-poc.test` header, returned `hosting-lab-ok`. Kubernetes applied the hosting resource/security profile and NetworkPolicy.

`tests/mac-app-lifecycle-smoke.sh --apply` is a repeatable test inside the prepared VM. It requires the exact lab hostname, private `/root/lab-cli.json`, the synthetic endpoint/environment and a bound server identity. It deploys the healthy fixture, introduces a missing readiness path, verifies a new Pod's readiness-failure event while the original healthy Pod keeps its UID and restart count, checks ingress still serves traffic, and explicitly redeploys the healthy configuration. Cleanup attempts to restore the healthy fixture if the test fails during the bad rollout.

Prepare the image on the Mac and copy it without mounting host directories:

```sh
docker build --platform linux/arm64 -t hosting-lab-app:qualification deploy/qualification/app
docker save hosting-lab-app:qualification -o /tmp/hosting-lab-app.tar
limactl copy /tmp/hosting-lab-app.tar deployer-poc-lab:/tmp/
limactl copy deploy/qualification/app/deployer.yaml deployer-poc-lab:/tmp/hosting-lab-app.yaml
limactl copy tests/mac-app-lifecycle-smoke.sh deployer-poc-lab:/tmp/
limactl shell deployer-poc-lab -- sudo k3s ctr images import --no-unpack /tmp/hosting-lab-app.tar
limactl shell deployer-poc-lab -- sudo bash /tmp/mac-app-lifecycle-smoke.sh --apply
```

The login context must already exist, created with the synthetic bootstrap admin token through the CLI login prompt. Do not use production credentials. This test changes only the named lab app and requires the prepared namespace, quota and ingress controller.

The lifecycle test passed. After a guest `sync` and `systemctl reboot`, the platform services, Traefik and test application returned healthy and ingress again returned the expected body. This extends the earlier reboot evidence to an actual application. HTTP inside the guest does not qualify public HTTPS, certificate issuance or renewal, multi-node operation, full recovery, or external alert delivery.

The [two-VM worker rehearsal](mac-worker-qualification.md) extends this evidence to actual worker enrollment, repaired reboot persistence, cross-node app traffic and CLI drain/uncordon. Both VM definitions now use the shared Lima user-mode network.
