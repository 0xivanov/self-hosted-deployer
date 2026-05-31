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
  --wireguard-endpoint YOUR_VPS:51820
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
