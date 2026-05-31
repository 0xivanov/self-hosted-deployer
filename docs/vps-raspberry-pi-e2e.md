# VPS And Raspberry Pi End-To-End Setup

This tutorial walks through a real MVP setup with:

- one VPS as the deployer control plane, WireGuard hub, and k3s server
- one Raspberry Pi as a k3s worker
- one demo application deployed onto the Pi
- optional public access through port-forwarding or ingress

The commands assume Debian/Ubuntu-style hosts and a Raspberry Pi running 64-bit Linux (`aarch64`, Go arch `linux/arm64`).

## Topology

```text
operator laptop
  -> deployer CLI
  -> VPS public IP:7443

VPS
  -> deployer-server
  -> k3s server
  -> WireGuard hub: 10.8.0.1/24

Raspberry Pi
  -> deployer-agent
  -> k3s agent
  -> WireGuard peer: 10.8.0.x/32
  -> application pods
```

Public traffic, once routing is configured, is intended to flow like:

```text
browser -> VPS public IP:80/443 -> k3s ingress -> service -> pod on Raspberry Pi
```

For early testing without DNS, use `kubectl port-forward` on the VPS.

## Prerequisites

You need:

- SSH access to the VPS and Raspberry Pi.
- This repository cloned on the operator machine, VPS, and Pi.
- Go installed on hosts where you build from source.
- TCP `7443` open on the VPS for deployer gRPC.
- UDP `51820` open on the VPS for WireGuard.
- TCP `80` and `443` open later if testing ingress.
- TCP `8080` open only if using the port-forward example from your laptop.

Use `http://<vps-ip>:7443` unless you configured `DEPLOYER_SERVER_TLS_CERT_FILE` and `DEPLOYER_SERVER_TLS_KEY_FILE`. The default tutorial path does not configure gRPC TLS.

The repository `.env` is for local development. A real systemd install uses:

- `/etc/deployer/server.env` on the VPS
- `/etc/deployer/agent.env` on each worker

## 1. Prepare WireGuard On The VPS

Install WireGuard tools:

```bash
sudo apt update
sudo apt install -y wireguard wireguard-tools
```

Generate the hub keypair:

```bash
sudo install -d -m 700 /etc/wireguard
wg genkey | sudo tee /etc/wireguard/privatekey | wg pubkey | sudo tee /etc/wireguard/publickey
sudo chmod 600 /etc/wireguard/privatekey
```

The command prints the public key at the end. You can verify the pair:

```bash
sudo cat /etc/wireguard/privatekey | wg pubkey
sudo cat /etc/wireguard/publickey
```

Create `/etc/wireguard/wg0.conf`:

```bash
sudo nano /etc/wireguard/wg0.conf
```

Put this in the file, replacing `VPS_PRIVATE_KEY`:

```ini
[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PrivateKey = VPS_PRIVATE_KEY
```

Get `VPS_PRIVATE_KEY` with:

```bash
sudo cat /etc/wireguard/privatekey
```

Start the hub:

```bash
sudo systemctl enable --now wg-quick@wg0
sudo wg show
```

Expected:

```text
interface: wg0
  public key: ...
  listening port: 51820
```

## 2. Install And Bootstrap The VPS Control Plane

On the VPS:

```bash
cd ~/self-hosted-deployer
make build

sudo install -m 0755 bin/deployer-server /usr/local/bin/deployer-server
sudo install -m 0644 deploy/systemd/deployer-server.service /etc/systemd/system/deployer-server.service
```

Bootstrap server config and the initial admin token:

```bash
sudo deployer-server bootstrap server \
  --env-file /etc/deployer/server.env \
  --database-url file:/var/lib/deployer/deployer.db \
  --public-base-url http://VPS_PUBLIC_IP:7443 \
  --grpc-addr :7443 \
  --http-addr :7080 \
  --k3s-wireguard-ip 10.8.0.1 \
  --wireguard-hub-public-key "$(sudo cat /etc/wireguard/publickey)" \
  --wireguard-endpoint VPS_PUBLIC_IP:51820
```

Save the printed `dep_admin_...` token. It is shown once.

Bootstrap k3s on the VPS and start deployer-server:

```bash
sudo deployer-server bootstrap k3s --wireguard-ip 10.8.0.1
sudo systemctl daemon-reload
sudo systemctl enable --now deployer-server.service
```

Verify:

```bash
sudo systemctl status deployer-server --no-pager
sudo k3s kubectl get nodes -o wide
sudo ss -ltnp | grep 6443
```

Expected:

- `deployer-server.service` is active.
- The VPS appears as a `Ready` k3s control-plane node.
- k3s listens on `10.8.0.1:6443`.

## 3. Configure The Operator CLI

On the operator machine, or on the VPS if you are testing from there:

```bash
cd /path/to/self-hosted-deployer
make build
```

Test the control plane directly:

```bash
./bin/deployer --server http://VPS_PUBLIC_IP:7443 --token dep_admin_... server status
```

Or save the config:

```bash
./bin/deployer --token dep_admin_... login http://VPS_PUBLIC_IP:7443
./bin/deployer server status
./bin/deployer doctor
```

If you prefer a global command:

```bash
make install-cli
export PATH="$HOME/.local/bin:$PATH"
```

## 4. Prepare The Raspberry Pi

On the Pi, confirm it is 64-bit:

```bash
uname -m
```

Expected for this tutorial:

```text
aarch64
```

Install prerequisites:

```bash
sudo apt update
sudo apt install -y wireguard wireguard-tools curl
```

Enable memory cgroups if k3s needs them. First check:

```bash
cat /sys/fs/cgroup/cgroup.controllers
```

If `memory` is missing, edit the Pi boot command line:

```bash
BOOT_CMDLINE=/boot/firmware/cmdline.txt
[ -f "$BOOT_CMDLINE" ] || BOOT_CMDLINE=/boot/cmdline.txt
sudo cp "$BOOT_CMDLINE" "$BOOT_CMDLINE.bak"
sudo nano "$BOOT_CMDLINE"
```

Keep the file as one line and append:

```text
cgroup_enable=cpuset cgroup_enable=memory cgroup_memory=1
```

Reboot the Pi and verify that `memory` appears:

```bash
sudo reboot
cat /sys/fs/cgroup/cgroup.controllers
```

Build the agent:

```bash
cd ~/self-hosted-deployer
make build
```

## 5. Enroll The Pi

Create a join token from the operator machine:

```bash
./bin/deployer nodes add --arch linux/arm64 pi-home
```

Save the printed `dep_join_...` token.

On the Pi:

```bash
cd ~/self-hosted-deployer

sudo ./scripts/install-agent.sh \
  --server http://VPS_PUBLIC_IP:7443 \
  --token dep_join_... \
  --agent-binary ./bin/deployer-agent
```

The installer:

- installs `/usr/local/bin/deployer-agent`
- writes `/etc/deployer/agent.env`
- runs `deployer-agent join`
- writes WireGuard key material under `/etc/deployer/wireguard/`
- runs `deployer-agent join-k3s`
- installs and starts `deployer-agent.service`

The k3s download can take several minutes on a Pi.

## 6. Verify The Private Network And Worker

On the Pi:

```bash
sudo wg show
ip addr show wg0
curl -k --connect-timeout 5 https://10.8.0.1:6443/cacerts
sudo systemctl status k3s-agent --no-pager
sudo systemctl status deployer-agent --no-pager
```

Expected:

- `wg0` has an address like `10.8.0.2/32` or `10.8.0.3/32`.
- `curl -k https://10.8.0.1:6443/cacerts` returns certificate text.
- `k3s-agent.service` is active.
- `deployer-agent.service` is active.

On the VPS:

```bash
sudo wg show
sudo k3s kubectl get nodes -o wide
```

Expected:

- The VPS WireGuard hub has a peer for the Pi.
- k3s shows the Pi as `Ready`, with internal IP `10.8.0.x`.

From the operator CLI:

```bash
./bin/deployer nodes inspect pi-home
./bin/deployer doctor
```

Expected:

- Node status is `online`.
- Kubernetes readiness is `ready`.
- VPN status is `connected`.
- Kubernetes workers check passes.

## 7. Deploy The Smoke Application

The smoke application is `nginx:alpine`, a small HTTP server listening on port `80`.

If using `./bin/deployer` rather than an installed `deployer` command:

```bash
export DEPLOYER_BIN=./bin/deployer
```

Run the smoke test:

```bash
export DEPLOYER_SERVER_URL=http://VPS_PUBLIC_IP:7443
export DEPLOYER_ADMIN_TOKEN=dep_admin_...
export DEPLOYER_SMOKE_ARCH=linux/arm64

./scripts/smoke-test.sh
```

Verify:

```bash
./bin/deployer status deployer-smoke
./bin/deployer logs --tail 50 deployer-smoke
sudo k3s kubectl -n deployer-apps get pods -o wide
```

Expected status:

```text
HEALTHY   DESIRED
1         1

REPLICAS
pi-home healthy
```

Expected logs include nginx startup and Kubernetes health probes returning `200`.

## 8. Access The App Without DNS

Without a domain, use port-forwarding from the VPS:

```bash
sudo k3s kubectl -n deployer-apps port-forward svc/deployer-smoke 8080:80 --address 0.0.0.0
```

Then open:

```text
http://VPS_PUBLIC_IP:8080
```

This should show the default nginx welcome page.

This path is temporary: traffic works only while the `port-forward` command is running.

## 9. Deploy Your Own App

Create a `deployer.yaml`:

```yaml
name: my-api
image: ghcr.io/yourname/my-api:latest
service:
  port: 3000
  health:
    path: /health
routing: {}
deploy:
  replicas: 1
placement:
  arch: linux/arm64
state:
  mode: stateless
resilience:
  mode: basic
```

Deploy:

```bash
./bin/deployer deploy --file deployer.yaml
./bin/deployer status my-api
./bin/deployer logs --tail 50 my-api
```

Port-forward for manual access:

```bash
sudo k3s kubectl -n deployer-apps port-forward svc/my-api 8080:3000 --address 0.0.0.0
```

Use an image that supports `linux/arm64`, or Kubernetes will not be able to run it on a Raspberry Pi.

## 10. Make The App Public With DNS

Point a DNS record at the VPS public IP:

```text
app.example.com -> VPS_PUBLIC_IP
```

Open TCP `80` and `443` on the VPS.

Deploy with `routing.domain`:

```yaml
name: my-api
image: ghcr.io/yourname/my-api:latest
service:
  port: 3000
  health:
    path: /health
routing:
  domain: app.example.com
deploy:
  replicas: 1
placement:
  arch: linux/arm64
state:
  mode: stateless
resilience:
  mode: basic
```

Then:

```bash
./bin/deployer deploy --file deployer.yaml
./bin/deployer routes list
./bin/deployer status my-api
```

k3s usually installs Traefik by default. Check ingress components with:

```bash
sudo k3s kubectl get pods -A
sudo k3s kubectl get svc -A
```

## Add Another Raspberry Pi

Repeat the worker enrollment with a new node name:

```bash
./bin/deployer nodes add --arch linux/arm64 pi-garage
```

On the second Pi:

```bash
cd ~/self-hosted-deployer
make build

sudo apt install -y wireguard wireguard-tools curl

sudo ./scripts/install-agent.sh \
  --server http://VPS_PUBLIC_IP:7443 \
  --token dep_join_... \
  --agent-binary ./bin/deployer-agent
```

Expected:

- The second Pi receives the next WireGuard IP, for example `10.8.0.4`.
- The VPS WireGuard hub gets another peer.
- k3s shows another `Ready` worker.
- `deployer doctor` still passes.

For a multi-replica app:

```yaml
deploy:
  replicas: 2
placement:
  arch: linux/arm64
  spread: true
```

Check placement:

```bash
sudo k3s kubectl -n deployer-apps get pods -o wide
```

## Troubleshooting

### `deployer: command not found` During Smoke Test

Set `DEPLOYER_BIN`:

```bash
export DEPLOYER_BIN=./bin/deployer
./scripts/smoke-test.sh
```

Or install the CLI:

```bash
make install-cli
export PATH="$HOME/.local/bin:$PATH"
```

### `deployer logs <app> [--tail lines]` Prints Usage

The current CLI parser expects flags before the app name:

```bash
./bin/deployer logs --tail 50 deployer-smoke
```

### k3s Agent Fails With `failed to find memory cgroup (v2)`

Enable Pi memory cgroups as shown in [Prepare The Raspberry Pi](#4-prepare-the-raspberry-pi), then reboot.

### Pi Times Out Fetching `https://10.8.0.1:6443/cacerts`

Check from the Pi:

```bash
sudo wg show
ip addr show wg0
curl -k -v --connect-timeout 5 https://10.8.0.1:6443/cacerts
```

Check from the VPS:

```bash
sudo ss -ltnp | grep 6443
curl -k -v --connect-timeout 5 https://10.8.0.1:6443/cacerts
sudo wg show
```

If the VPS local curl works but the Pi curl times out, compare WireGuard keys.

On the Pi:

```bash
sudo cat /etc/deployer/wireguard/privatekey | wg pubkey
sudo wg show
```

On the VPS:

```bash
sudo wg show
```

The Pi interface public key must match the VPS peer for the Pi's `AllowedIPs`.

If an interrupted install regenerated or changed the Pi key, update the stored key on the VPS:

```bash
sudo apt update
sudo apt install -y sqlite3
sudo cp /var/lib/deployer/deployer.db /var/lib/deployer/deployer.db.bak

sudo sqlite3 /var/lib/deployer/deployer.db \
  "UPDATE nodes SET wireguard_public_key='PI_CURRENT_PUBLIC_KEY' WHERE name='pi-home';"

sudo wg set wg0 peer 'PI_CURRENT_PUBLIC_KEY' allowed-ips 10.8.0.3/32
sudo wg set wg0 peer 'OLD_PUBLIC_KEY' remove
```

Then retry from the Pi:

```bash
curl -k --connect-timeout 5 https://10.8.0.1:6443/cacerts
sudo systemctl restart k3s-agent
```

### Stale Or Mistyped Nodes

Remove stale nodes from the operator CLI:

```bash
./bin/deployer nodes remove --yes pi-homne
```

Then check:

```bash
./bin/deployer nodes list
./bin/deployer doctor
```
