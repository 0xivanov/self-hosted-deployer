# Operations

This phase makes the MVP installable enough for manual VPS and Linux worker testing.

For the full manual path from a fresh VPS and Raspberry Pi to a deployed nginx smoke app, see [VPS And Raspberry Pi End-To-End Setup](vps-raspberry-pi-e2e.md).

## Install Paths

- Binaries: `/usr/local/bin/deployer`, `/usr/local/bin/deployer-server`, `/usr/local/bin/deployer-agent`
- Server env file: `/etc/deployer/server.env`
- Agent env file: `/etc/deployer/agent.env`
- Server database: `/var/lib/deployer/deployer.db`
- Systemd units:
  - `/etc/systemd/system/deployer-server.service`
  - `/etc/systemd/system/deployer-agent.service`

## VPS Setup

Build or unpack a Linux release archive, then install the server binary and unit:

```bash
sudo install -m 0755 deployer-server /usr/local/bin/deployer-server
sudo install -m 0644 deploy/systemd/deployer-server.service /etc/systemd/system/deployer-server.service
sudo deployer-server bootstrap server \
  --env-file /etc/deployer/server.env \
  --database-url file:/var/lib/deployer/deployer.db \
  --public-base-url https://YOUR_VPS:7443 \
  --k3s-wireguard-ip 10.8.0.1 \
  --wireguard-hub-public-key YOUR_HUB_PUBLIC_KEY \
  --wireguard-endpoint YOUR_VPS:51820 \
  --ingress-acme-email you@example.com
```

The bootstrap command prints the initial admin token once. Store it in your local CLI config with:

```bash
deployer --token dep_admin_... login https://YOUR_VPS:7443
```

Then bootstrap k3s and start the server:

```bash
sudo deployer-server bootstrap k3s --wireguard-ip 10.8.0.1
sudo systemctl daemon-reload
sudo systemctl enable --now deployer-server.service
```

To enable HTTPS for routed apps, install cert-manager before deploying routes and set `--ingress-acme-email` during `bootstrap server`:

```bash
sudo k3s kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.18.2/cert-manager.yaml
```

When `DEPLOYER_INGRESS_ACME_EMAIL` is set, the server creates a cert-manager `ClusterIssuer` named `deployer-letsencrypt` and each routed app gets a TLS Ingress with a per-app certificate secret. On an existing install, add `DEPLOYER_INGRESS_ACME_EMAIL=you@example.com` to `/etc/deployer/server.env`, install cert-manager, restart `deployer-server`, and redeploy the app route. Leave the Cloudflare record as DNS only while HTTP-01 certificates are being issued.

## Worker Setup

Create a join token from your local machine:

```bash
deployer nodes add pi-kitchen
```

On the worker, unpack the Linux ARM64 archive and run:

```bash
sudo ./scripts/install-agent.sh \
  --server https://YOUR_VPS:7443 \
  --token dep_join_... \
  --agent-binary ./deployer-agent
```

The installer writes `/etc/deployer/agent.env`, runs `deployer-agent join`, runs `deployer-agent join-k3s`, installs the systemd unit, and starts `deployer-agent.service`.

On Raspberry Pi hosts where the kernel memory cgroup is disabled, the installer updates `/boot/firmware/cmdline.txt` or `/boot/cmdline.txt` with `cgroup_memory=1 cgroup_enable=memory` before enrollment and exits. Reboot the Pi, then rerun the installer with the same unexpired join token or create a fresh one with `deployer nodes add`.

## Node Cleanup

Use soft removal when you want to revoke a worker and keep its historical record:

```bash
deployer nodes remove pi-kitchen
```

Soft-removed nodes keep their name and WireGuard IP reserved. To permanently delete a removed or failed enrollment and make the name/IP reusable, purge it:

```bash
deployer nodes purge pi-kitchen
deployer nodes purge --yes pi-kitchen
```

Pending or removed nodes can be renamed before retrying enrollment:

```bash
deployer nodes rename pi-kithcen pi-kitchen
deployer nodes add pi-kitchen
```

Renaming deletes outstanding join tokens for the old name, so create a new token before rerunning the agent installer. Active Kubernetes nodes cannot be renamed in place because Kubernetes node names are immutable. Purge and rejoin the worker under the new name instead.

## Doctor

Use `deployer doctor` after login to check the control plane, readiness, enrolled nodes, WireGuard heartbeat status, and ingress routes:

```bash
deployer doctor
deployer --output json doctor
```

## Smoke Test

For a real VPS smoke test, set:

```bash
export DEPLOYER_SERVER_URL=https://YOUR_VPS:7443
export DEPLOYER_ADMIN_TOKEN=dep_admin_...
export DEPLOYER_SMOKE_ARCH=linux/arm64
./scripts/smoke-test.sh
```

If your only schedulable worker is amd64, set `DEPLOYER_SMOKE_ARCH=linux/amd64`.

## Release Artifacts

GitHub Actions builds release artifacts when a tag matching `v*` is pushed.

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow runs tests, builds `make release` with `VERSION` set to the tag, verifies checksums, and uploads:

- `deployer-darwin-arm64.tar.gz`
- `deployer-linux-amd64.tar.gz`
- `deployer-linux-arm64.tar.gz`
- `checksums.txt`

Use `deployer-linux-amd64.tar.gz` for amd64 VPS hosts and `deployer-linux-arm64.tar.gz` for 64-bit Raspberry Pi workers.

## Updating From Release Artifacts

After a release exists, hosts can update directly from GitHub without a local Go toolchain or git checkout.

On the VPS:

```bash
VERSION=v0.1.0
curl -fsSL "https://raw.githubusercontent.com/0xivanov/self-hosted-deployer/$VERSION/scripts/install-release.sh" -o /tmp/install-release.sh
sudo sh /tmp/install-release.sh --version "$VERSION" --role server
```

On a Raspberry Pi worker:

```bash
VERSION=v0.1.0
curl -fsSL "https://raw.githubusercontent.com/0xivanov/self-hosted-deployer/$VERSION/scripts/install-release.sh" -o /tmp/install-release.sh
sudo sh /tmp/install-release.sh --version "$VERSION" --role agent
```

The installer auto-detects `linux-amd64` versus `linux-arm64`, verifies the tarball against `checksums.txt`, installs binaries into `/usr/local/bin`, updates the matching systemd unit, runs `systemctl daemon-reload`, and restarts the selected service. Use `--version latest` to follow the latest GitHub release.
