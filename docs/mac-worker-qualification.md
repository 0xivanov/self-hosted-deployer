# Mac worker qualification

The disposable control plane and worker use Lima's shared `user-v2` network. This user-mode network requires no Mac system networking installation and exposes no application ports to the public network. Both VM definitions disable host directory mounts and SSH agent forwarding.

The control plane uses `deploy/qualification/mac-vm.yaml` (2 CPUs, 3 GiB RAM, 12 GiB sparse disk). The worker uses `deploy/qualification/mac-worker-vm.yaml` (1 CPU, 2 GiB RAM, 10 GiB sparse disk). Together they model one synthetic customer environment, not two isolated customers.

```sh
limactl start --name=deployer-poc-worker --tty=false deploy/qualification/mac-worker-vm.yaml
```

For an existing lab control plane created before shared networking was added, stop it before applying the network change:

```sh
limactl shell deployer-poc-lab -- sudo sync
limactl stop deployer-poc-lab
limactl edit --tty=false --network lima:user-v2 deployer-poc-lab
limactl start --tty=false deployer-poc-lab
```

## Enrollment evidence, September 8

The worker resolved `lima-deployer-poc-lab.internal` on the shared network. Its `/etc/hosts` mapped the synthetic certificate hostname `deployer-poc.test` to that address, and only the lab CA was added to its trust store. Obtain the actual address from guest DNS rather than assuming it stays fixed. No production credentials or backup keys were used.

The worker had `wireguard-tools`, curl and CA certificates installed before enrollment. The real ARM64 agent built at commit `e3d1542` was copied into the guest. The k3s `v1.35.5+k3s1` binary was copied from the lab control plane and verified against SHA-256 `aced90bea6a4e025abef4cf6db7d8cc316a7d20a2b963f809852a37f2b9dbc03`. The lab HTTPS artifact server supplied the installer with SHA-256 `e5cc3b3d9dfc1662c2d9be6da5abc9a4cd317d6abc3a5ffc02e3dd3248207fee`. `INSTALL_K3S_SKIP_DOWNLOAD=true` kept worker installation on that binary.

The real CLI created a one-time join token for `mac-lab-worker`; private output was captured outside Git. `scripts/install-agent.sh` completed successfully against the synthetic management endpoint. Both nodes became Ready. Node inspection reported the worker online, Kubernetes ready, and VPN connected on `10.81.0.2`. The agent environment persisted the correct `10.81.0.1` hub rather than the legacy default.

The next reboot exposed a persistence failure: the WireGuard interface disappeared, `wg-quick@wg0` was disabled, k3s-agent remained activating, and the live heartbeat reported VPN disconnected. Application traffic on the control plane remained healthy. This is why a successful join alone does not establish reboot-safe onboarding.

## Boundary

This lab establishes actual enrollment over HTTPS and WireGuard on ARM64, not provider firewall behavior or separate-customer isolation. The installer fix enables `wg-quick@wg0` for boot and adds a k3s-agent systemd dependency on that interface. The exact generated steps were applied to the already-enrolled synthetic worker, then a second guest reboot passed: WireGuard and k3s-agent became active, the node returned Ready, and a fresh heartbeat reported VPN connected. This tests the persistence repair, not a second complete fresh install of the revised script.

With the control plane temporarily cordoned, a rolling restart moved the test application onto `mac-lab-worker`. After the old Pod disappeared, its EndpointSlice contained only the worker Pod IP `10.180.1.4`. HTTP through the control-plane ingress still returned `hosting-lab-ok`, exercising Traefik to worker traffic over the WireGuard/flannel path with the hosting NetworkPolicy enabled. The control plane was uncordoned afterward.

The real CLI then drained `mac-lab-worker`. Kubernetes moved the single application replica back to the control plane. One HTTP probe returned 503 before the replacement became Ready; subsequent probes returned the expected body. The worker was uncordoned and both nodes were Ready. This is a successful drain/recovery exercise with a brief interruption, not zero-downtime worker failover.

Shell syntax and ShellCheck passed for the revised installer. Both disposable VMs are stopped at the end of the rehearsal with their disks retained. Public application TLS, full environment restore, alert delivery and pilot duration remain independent gates.
