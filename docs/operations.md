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

## CloudNativePG Setup

Apps using `database.postgres` require CloudNativePG 1.30.0 and Kubernetes
1.34 through 1.36. Install the checksum-pinned official operator manifest from
the repository checkout on the k3s server:

```bash
sudo ./scripts/install-cnpg.sh
```

The script uses `k3s kubectl` when run as root on a k3s server, verifies the
manifest checksum, replaces the operator tag with its immutable multi-platform
digest before server-side apply, and waits for both required CRDs and the
operator to be ready. It refuses to overwrite an existing operator using a
different image, and is safe to rerun with the same pinned version. A host
using a normal Kubernetes context can run:

```bash
./scripts/install-cnpg.sh --client kubectl
```

Before migrating production data, verify three same-architecture Ready nodes,
durable storage, independent backups with a restore drill, quorum network
reachability, and a write-quiescing cutover plan. Database replicas alone do
not make a single-server k3s control plane or hub-and-spoke WireGuard network
highly available. See [PostgreSQL High Availability](postgres-ha.md) for the
configuration, failure semantics, migration gates, retention behavior, and
rollback procedure.

Managed database connections enforce TLS, SCRAM-SHA-256, and pgx/libpq-style
channel binding. Normal app deployment freezes database image, capacity,
storage, and replication policy; use a dedicated CloudNativePG maintenance
runbook for those operations.

## Monitoring Setup

Apps can opt into Prometheus discovery with a private metrics listener:

```yaml
metrics:
  port: 9090
  path: /metrics
```

The metrics port must differ from `service.port`. The deployer adds it to the
pod and adds Prometheus discovery annotations, but does not add it to the app
Service or public Ingress.

Install the pinned Prometheus, Alertmanager, Loki, Alloy, Grafana, and
kube-state-metrics stack on the k3s control-plane node:

```bash
sudo ./scripts/install-monitoring.sh
```

This keeps every Service cluster-private. Grafana is available through port
forwarding unless a public hostname is provided. To create an HTTPS route with
local Grafana authentication, point the hostname at the VPS and run:

```bash
install -m 0600 /dev/null /tmp/grafana-password
editor /tmp/grafana-password
sudo ./scripts/install-monitoring.sh \
  --grafana-domain grafana.example.com \
  --grafana-admin-user admin \
  --grafana-admin-password-file /tmp/grafana-password
rm /tmp/grafana-password
```

The password must contain 16 through 128 characters. When no password file is
provided on the first install, the installer creates a random administrator
password in the `grafana-admin` Secret. Retrieve it from a trusted terminal:

```bash
sudo k3s kubectl -n deployer-monitoring get secret grafana-admin \
  -o jsonpath='{.data.admin-password}' | base64 -d
echo
```

Grafana disables anonymous access and user sign-up, uses secure strict-same-site
cookies, and is exposed only through a cert-manager TLS Ingress. Prometheus,
Loki, Alloy, and Alertmanager remain inaccessible from the public Ingress.

This first install uses a discard receiver unless an Alertmanager config is
provided. To enable email, copy the example outside the repository, replace the
SMTP and recipient values, restrict its permissions, and install it:

```bash
install -m 0600 deploy/monitoring/alertmanager-email.example.yml /tmp/alertmanager.yml
editor /tmp/alertmanager.yml
sudo ./scripts/install-monitoring.sh --alertmanager-config /tmp/alertmanager.yml
rm /tmp/alertmanager.yml
```

The SMTP password is stored in the `alertmanager-config` Kubernetes Secret. It
must never be committed. Any SMTP account supporting authenticated TLS works.
For a free personal setup, a mailbox provider's SMTP endpoint and app password
are sufficient.

Prometheus retains up to 15 days or 4 GB, whichever is reached first. Loki uses
TSDB indexes and filesystem chunks with seven-day retention. Alloy collects pod
logs through the Kubernetes API and stores its read positions persistently.
Grafana provisions Prometheus and Loki data sources plus Money Manager Backend
and Cluster Logs dashboards from version-controlled files. Active Alertmanager
alerts are displayed through Prometheus's `ALERTS` metric.

Alertmanager, Prometheus, Loki, Alloy, and Grafana use local-path volumes pinned
to the control-plane node. Use port forwarding when inspecting private
interfaces:

```bash
sudo k3s kubectl -n deployer-monitoring port-forward service/prometheus 9090:9090
sudo k3s kubectl -n deployer-monitoring port-forward service/alertmanager 9093:9093
sudo k3s kubectl -n deployer-monitoring port-forward service/grafana 3000:3000
```

The default rules alert on unavailable deployments, failed scrapes, readiness,
sustained error rate and latency, restart loops, stale maintenance workers, and
failed notification delivery. Thresholds are intentionally sustained with
`for` windows to avoid single-scrape noise.

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

The release workflow runs tests, verifies that committed protobuf output is
reproducible, builds `make release` from the exact tag with deterministic build
metadata, and verifies checksums. It uploads all assets to a draft and only
then publishes the release. Existing releases are immutable and never
overwritten.

- `deployer-darwin-arm64.tar.gz`
- `deployer-linux-amd64.tar.gz`
- `deployer-linux-arm64.tar.gz`
- `install-release.sh`
- `install-cnpg.sh`
- `install-monitoring.sh`
- `checksums.txt`

Use `deployer-linux-amd64.tar.gz` for amd64 VPS hosts and `deployer-linux-arm64.tar.gz` for 64-bit Raspberry Pi workers.

## Updating From Release Artifacts

After a release exists, hosts can update directly from GitHub without a local
Go toolchain or git checkout. Bootstrap the installer from the checksummed
release assets, not from a mutable raw Git tag:

```bash
VERSION=v0.1.0
INSTALLER_DIR=$(mktemp -d)
BASE_URL="https://github.com/0xivanov/self-hosted-deployer/releases/download/$VERSION"
curl -fsSL "$BASE_URL/install-release.sh" -o "$INSTALLER_DIR/install-release.sh"
curl -fsSL "$BASE_URL/checksums.txt" -o "$INSTALLER_DIR/checksums.txt"
(
  cd "$INSTALLER_DIR"
  grep '  install-release.sh$' checksums.txt > install-release.sh.sha256
  sha256sum -c install-release.sh.sha256
)
```

On the VPS:

```bash
sudo sh "$INSTALLER_DIR/install-release.sh" --version "$VERSION" --role server
```

On a Raspberry Pi worker:

```bash
sudo sh "$INSTALLER_DIR/install-release.sh" --version "$VERSION" --role agent
```

The installer auto-detects `linux-amd64` versus `linux-arm64`, verifies the tarball against `checksums.txt`, installs binaries into `/usr/local/bin`, updates the matching systemd unit, runs `systemctl daemon-reload`, and restarts the selected service. Use `--version latest` to follow the latest GitHub release.
Custom `--install-dir` values are CLI-only because the systemd services use
`/usr/local/bin`.

The installer also enables a role-specific systemd timer. Servers check for a new GitHub Release every 15 minutes near minute 0. Agents check near minute 5 with randomized delay so the server normally updates first and workers do not restart together.

```bash
systemctl list-timers 'deployer-auto-update*'
journalctl -u deployer-auto-update-server.service
journalctl -u deployer-auto-update-agent.service
```

The updater compares the installed version with the latest release, refuses
non-forward version moves, and invokes the trusted local
`/usr/local/sbin/deployer-install-release` installed from the previous
checksummed package. It serializes both automatic and manual installs with a
host lock and restores all previously installed release files if installation
or the restarted service health check fails. A failed release is quarantined
on that host so the timer does not retry it; a newer release clears the
quarantine automatically.
