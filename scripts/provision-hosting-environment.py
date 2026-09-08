#!/usr/bin/env python3
"""Provision one operator-owned, dedicated deployer environment."""
from __future__ import annotations
import fcntl
import argparse, base64, hashlib, ipaddress, json, os, re, shlex, subprocess, sys
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlsplit
from typing import Any, Iterable

class ProvisioningError(ValueError):
    pass

REQUIRED = ("environment", "customer", "ssh", "release", "network", "domain", "admin_endpoint", "resource_budget", "backup", "recovery")
SECRET_WORDS = re.compile(r"(?i)(token|password|secret|private[_-]?key|credential)\s*[=:]\s*[^\s,]+")
TOKEN = re.compile(r"dep_(?:admin|join)_[A-Za-z0-9_-]+")

def _simple_yaml(raw: str) -> Any:
    try:
        import yaml  # type: ignore
    except ImportError as exc:
        raise ProvisioningError("YAML manifest requires PyYAML; use JSON or install PyYAML") from exc
    return yaml.safe_load(raw)

def _load_data(path: Path) -> Any:
    raw = path.read_text(encoding="utf-8")
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return _simple_yaml(raw)

def _required(mapping: dict[str, Any], name: str) -> Any:
    value = mapping.get(name)
    if value is None or value == "" or value == {}:
        raise ProvisioningError(f"manifest requires {name}")
    return value

def _cidrs(values: Iterable[str], label: str) -> list[ipaddress._BaseNetwork]:
    result = []
    for value in values:
        try:
            result.append(ipaddress.ip_network(value, strict=True))
        except ValueError as exc:
            raise ProvisioningError(f"{label} contains invalid CIDR {value!r}") from exc
    return result

def validate_manifest(manifest: dict[str, Any], registry: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    for key in REQUIRED:
        _required(manifest, key)
    env, customer, ssh, release, network = (manifest[k] for k in ("environment", "customer", "ssh", "release", "network"))
    if not isinstance(env, dict) or not re.fullmatch(r"[a-z][a-z0-9-]{2,47}", str(env.get("id", ""))):
        raise ProvisioningError("environment.id must be 3-48 lowercase letters, digits, or hyphens")
    if not isinstance(customer, dict) or not str(customer.get("id", "")).strip() or not str(customer.get("label", "")).strip():
        raise ProvisioningError("customer.id and customer.label are required")
    if not isinstance(ssh, dict) or not str(ssh.get("host", "")).strip() or not str(ssh.get("user", "")).strip():
        raise ProvisioningError("ssh.host and ssh.user are required")
    if not re.fullmatch(r"[a-z_][a-z0-9_-]*", ssh["user"]) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.-]*", ssh["host"]):
        raise ProvisioningError("ssh requires a plain username and hostname, IPv4 address or alias")
    if int(ssh.get("port", 22)) not in range(1, 65536):
        raise ProvisioningError("ssh.port must be 1-65535")
    if not isinstance(release, dict) or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", str(release.get("version", ""))):
        raise ProvisioningError("release.version must be a pinned semantic version such as v0.1.0")
    for checksum_name in ("artifact_sha256",):
        checksum = str(release.get(checksum_name, ""))
        if not re.fullmatch(r"[0-9a-fA-F]{64}", checksum):
            raise ProvisioningError(f"release.{checksum_name} must be a 64 character SHA-256 checksum")
    if not re.fullmatch(r"deployer-linux-(amd64|arm64)\.tar\.gz", str(release.get("asset", ""))):
        raise ProvisioningError("release.asset must be deployer-linux-amd64.tar.gz or deployer-linux-arm64.tar.gz")
    if "base_url" in release:
        if not isinstance(release["base_url"], str) or not release["base_url"]:
            raise ProvisioningError("release.base_url must be a nonempty HTTPS URL")
        artifact_origin = urlsplit(release["base_url"])
        if artifact_origin.scheme != "https" or not artifact_origin.hostname or artifact_origin.username or artifact_origin.password or artifact_origin.query or artifact_origin.fragment:
            raise ProvisioningError("release.base_url must be HTTPS without credentials, query or fragment")
    if not isinstance(network, dict):
        raise ProvisioningError("network must be an object")
    networks = _cidrs([str(_required(network, k)) for k in ("wireguard_cidr", "pod_cidr", "service_cidr")], "network")
    _required(network, "wireguard_endpoint")
    if not re.fullmatch(r"[A-Za-z0-9_=+.-]{1,15}", str(network.get("wireguard_interface", "wg0"))):
        raise ProvisioningError("network.wireguard_interface is invalid")
    if not re.fullmatch(r"[^:]+:[0-9]{1,5}", str(network["wireguard_endpoint"])) or int(str(network["wireguard_endpoint"]).rsplit(":", 1)[1]) not in range(1, 65536):
        raise ProvisioningError("network.wireguard_endpoint must be host:port")
    k3s = _required(manifest, "k3s")
    if not isinstance(k3s, dict) or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:\+[0-9A-Za-z.-]+)?", str(k3s.get("version", ""))) or not re.match(r"^https://", str(k3s.get("installer_url", ""))) or not re.fullmatch(r"[0-9a-fA-F]{64}", str(k3s.get("installer_sha256", ""))):
        raise ProvisioningError("k3s.version, HTTPS k3s.installer_url, and k3s.installer_sha256 are required")
    if not re.fullmatch(r"[0-9a-fA-F]{64}", str(k3s.get("binary_sha256", ""))):
        raise ProvisioningError("k3s.binary_sha256 is required")
    if any(net.version != 4 or not any(net.subnet_of(ipaddress.ip_network(private)) for private in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")) for net in networks):
        raise ProvisioningError("use private IPv4 network ranges")
    if networks[0].prefixlen > 29 or networks[2].num_addresses < 16:
        raise ProvisioningError("WireGuard and service ranges are too small")
    hub = ipaddress.ip_address(network.get("wireguard_hub_ip", str(networks[0][1])))
    if hub not in networks[0] or hub in (networks[0].network_address, networks[0].broadcast_address):
        raise ProvisioningError("WireGuard hub IP must be usable within its network")
    tls = manifest.get("tls", {})
    for key in ("cert_path", "key_path"):
        value = tls.get(key, "")
        if not re.fullmatch(r"/etc/ssl/[A-Za-z0-9_./-]+", value) or ".." in value.split("/"):
            raise ProvisioningError("tls.cert_path and tls.key_path must identify preinstalled certificates under /etc/ssl")
    if any(a.overlaps(b) for i, a in enumerate(networks) for b in networks[i + 1:]):
        raise ProvisioningError("wireguard, pod, and service CIDRs must not overlap")
    if not isinstance(manifest["domain"], dict) or not str(manifest["domain"].get("base", "")).strip():
        raise ProvisioningError("domain.base is required")
    endpoint = urlsplit(str(manifest["admin_endpoint"]))
    if endpoint.scheme != "https" or not endpoint.hostname or endpoint.username or endpoint.password or endpoint.path or endpoint.query or endpoint.fragment:
        raise ProvisioningError("admin_endpoint must be an HTTPS origin")
    if endpoint.port is None or endpoint.port in (80, 443, 6443, 7080) or not 1 <= endpoint.port <= 65535:
        raise ProvisioningError("admin endpoint requires an explicit dedicated port, normally 7443")
    for section in ("resource_budget", "backup", "recovery"):
        if not isinstance(manifest[section], dict):
            raise ProvisioningError(f"{section} must be an object")
    for key in ("cpu", "memory", "ephemeral_storage", "pods"):
        _required(manifest["resource_budget"], key)
    if not re.fullmatch(r"[0-9]+m?", str(manifest["resource_budget"]["cpu"])) or not re.fullmatch(r"[0-9]+(Ki|Mi|Gi|Ti)", str(manifest["resource_budget"]["memory"])) or not re.fullmatch(r"[0-9]+(Ki|Mi|Gi|Ti)", str(manifest["resource_budget"]["ephemeral_storage"])) or not re.fullmatch(r"[1-9][0-9]*", str(manifest["resource_budget"]["pods"])):
        raise ProvisioningError("resource_budget quantities are invalid")
    if not manifest["backup"].get("scope") or not manifest["recovery"].get("targets"):
        raise ProvisioningError("backup.scope and recovery.targets are required")
    if manifest["backup"].get("scope") != "platform-database":
        raise ProvisioningError("only backup.scope=platform-database is currently executable; configure customer data backup separately")
    if manifest["backup"].get("destination") != "local-platform-backup":
        raise ProvisioningError("only backup.destination=local-platform-backup is currently executable; offsite backup is pending qualification")
    if registry:
        current = str(env["id"])
        for item in registry:
            if str(item.get("environment_id", item.get("id", ""))) == current:
                raise ProvisioningError(f"environment {current} is already present in the registry")
            other = _cidrs([str(item[k]) for k in ("wireguard_cidr", "pod_cidr", "service_cidr") if item.get(k)], "registry")
            for left in networks:
                if any(left.overlaps(right) for right in other):
                    raise ProvisioningError(f"network CIDR {left} overlaps registered environment {item.get('environment_id', item.get('id'))}")
    return manifest

READ_ONLY_INVENTORY = r'''set -eu
printf '%s\n' 'deployer-provisioning-inventory-v1'
uname -srm
command -v systemctl >/dev/null
[ "$(ps -p 1 -o comm= | tr -d ' ')" = systemd ]
printf '%s\n' 'init=systemd'
for path in /etc/wireguard /etc/deployer/server.env /etc/deployer/agent.env /etc/deployer /etc/rancher/k3s /var/lib/deployer /var/lib/rancher/k3s /etc/systemd/system/deployer-server.service /etc/systemd/system/deployer-agent.service; do
  ([ -e "$path" ] || [ -L "$path" ]) && printf 'path=%s\n' "$path" || true
done
for unit in deployer-server.service deployer-agent.service k3s.service k3s-agent.service; do
  systemctl show "$unit" --property=Id,LoadState,ActiveState --no-pager 2>/dev/null || true
done
command -v k3s >/dev/null 2>&1 && printf '%s\n' 'binary=k3s' || true
command -v deployer-server >/dev/null 2>&1 && printf '%s\n' 'binary=deployer-server' || true
hostnamectl status 2>/dev/null || true
'''

def refuse_existing_node(inventory: str) -> None:
    low = inventory.lower()
    if "deployer-provisioning-inventory-v1" not in low or "linux" not in low or "init=systemd" not in low:
        raise ProvisioningError("incomplete Linux inventory; refusing mutation")
    markers = ("path=/etc/wireguard", "path=/etc/deployer", "path=/etc/rancher/k3s", "path=/var/lib/deployer", "path=/var/lib/rancher/k3s", "path=/etc/systemd/system/deployer-server.service", "path=/etc/systemd/system/deployer-agent.service", "binary=k3s", "binary=deployer-server", "threehosts", "three-host")
    if any(marker in low for marker in markers) or ("loadstate=loaded" in low or "activestate=active" in low):
        raise ProvisioningError("remote host is already provisioned or resembles a legacy three-host installation; adoption is refused")

class Runner:
    def run(self, command: str, *, capture: bool = True) -> str:
        raise NotImplementedError

@dataclass
class SSHRunner(Runner):
    host: str
    user: str
    port: int = 22
    identity_file: str | None = None
    def run(self, command: str, *, capture: bool = True) -> str:
        args = ["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-p", str(self.port)]
        if self.identity_file:
            args += ["-i", self.identity_file]
        remote = ("bash -lc " + _q(command)) if self.user == "root" else ("sudo -n bash -lc " + _q(command))
        args += [f"{self.user}@{self.host}", remote]
        completed = subprocess.run(args, check=False, text=True, capture_output=capture)
        if completed.returncode:
            raise ProvisioningError(f"remote command failed with exit {completed.returncode}")
        return completed.stdout if capture else ""

def _q(value: str) -> str:
    return shlex.quote(str(value))

def build_plan(manifest: dict[str, Any]) -> list[str]:
    e, r, n = manifest["environment"], manifest["release"], manifest["network"]
    repo = r.get("repo", "0xivanov/self-hosted-deployer")
    version = r["version"]
    base = r.get("base_url", f"https://github.com/{repo}/releases/download/{version}").rstrip("/")
    asset = r["asset"]
    k3s = manifest["k3s"]
    hub_ip = str(n.get("wireguard_hub_ip", str(ipaddress.ip_network(n["wireguard_cidr"])[1])))
    namespace = "hosting-" + e["id"]
    iface = n.get("wireguard_interface", "wg0")
    budget = manifest["resource_budget"]
    work = "work=$(mktemp -d /tmp/deployer-provision.XXXXXX); trap 'rm -rf \"$work\"' EXIT"
    k3s_config = """write-kubeconfig: /etc/rancher/k3s/k3s.yaml
write-kubeconfig-mode: \"0600\"
bind-address: {hub}
advertise-address: {hub}
node-ip: {hub}
flannel-iface: {iface}
cluster-cidr: {pod}
service-cidr: {service}
cluster-dns: {dns}
tls-san:
  - {hub}
""".format(hub=hub_ip, iface=iface, pod=n["pod_cidr"], service=n["service_cidr"], dns=str(ipaddress.ip_network(n["service_cidr"])[10]))
    quota = json.dumps({"apiVersion": "v1", "kind": "ResourceQuota", "metadata": {"name": "hosting-budget", "namespace": namespace}, "spec": {"hard": {"requests.cpu": budget["cpu"], "limits.cpu": budget["cpu"], "requests.memory": budget["memory"], "limits.memory": budget["memory"], "requests.ephemeral-storage": budget["ephemeral_storage"], "limits.ephemeral-storage": budget["ephemeral_storage"], "pods": str(budget["pods"])}}}).encode()
    quota_b64 = base64.b64encode(quota).decode()
    config_b64 = base64.b64encode(k3s_config.encode()).decode()
    wg_command = "set -euo pipefail; umask 077; install -d -m 700 /etc/deployer/wireguard /etc/wireguard /etc/rancher/k3s; test ! -e /etc/deployer/wireguard/privatekey || { echo 'private key already exists; refusing reuse' >&2; exit 1; }; wg genkey > /etc/deployer/wireguard/privatekey; chmod 600 /etc/deployer/wireguard/privatekey; wg pubkey < /etc/deployer/wireguard/privatekey > /etc/deployer/wireguard/publickey; printf '[Interface]\\nAddress = " + hub_ip + "/" + str(ipaddress.ip_network(n["wireguard_cidr"]).prefixlen) + "\\nListenPort = " + str(n['wireguard_endpoint']).rsplit(':', 1)[-1] + "\\nPrivateKey = ' > /etc/wireguard/" + iface + ".conf; cat /etc/deployer/wireguard/privatekey >> /etc/wireguard/" + iface + ".conf; chmod 600 /etc/wireguard/" + iface + ".conf; systemctl enable --now " + _q("wg-quick@" + iface + ".service")
    return [
        f"READ ONLY: SSH inventory and adoption safety gate on {manifest['ssh']['host']}",
        "apt-get update && apt-get install -y curl ca-certificates wireguard wireguard-tools openssl",
        f"set -euo pipefail; umask 077; {work}; curl -fsSL {_q(base + '/' + asset)} -o \"$work/{asset}\"; echo {_q(r['artifact_sha256'] + '  ')}\"$work/{asset}\" | sha256sum -c -; tar -xzf \"$work/{asset}\" -C \"$work\"; package=\"$work/{asset.removesuffix('.tar.gz')}\"; install -d /usr/local/bin /usr/local/sbin; install -m 0755 \"$package/deployer\" \"$package/deployer-server\" /usr/local/bin/; install -m 0755 \"$package/scripts/auto-update.sh\" /usr/local/sbin/deployer-auto-update; install -m 0755 \"$package/scripts/install-release.sh\" /usr/local/sbin/deployer-install-release; install -m 0644 \"$package/deploy/systemd/deployer-server.service\" /etc/systemd/system/; install -d -m 0700 /etc/deployer; printf 'mode=manual\\n' > /etc/deployer/update-policy.conf; chmod 600 /etc/deployer/update-policy.conf",
        wg_command,
        "deployer-server bootstrap server --env-file /etc/deployer/server.env "
        f"--grpc-addr {_q(':' + str(urlsplit(manifest['admin_endpoint']).port or 443))} --http-addr 127.0.0.1:7080 --public-base-url {_q(manifest['admin_endpoint'])} --ingress-namespace {_q(namespace)} --k3s-wireguard-ip {_q(hub_ip)} "
        f"--wireguard-interface {_q(iface)} --wireguard-hub-public-key \"$(cat /etc/deployer/wireguard/publickey)\" --wireguard-endpoint {_q(n['wireguard_endpoint'])} "
        f"--ingress-acme-email {_q(manifest['domain'].get('acme_email', ''))}",
        f"set -euo pipefail; umask 077; openssl rand -hex 32 > /etc/deployer/server.identity; printf '%s\\n' {_q('DEPLOYER_WIREGUARD_SUBNET=' + n['wireguard_cidr'])} {_q('DEPLOYER_SERVER_IDENTITY_FILE=/etc/deployer/server.identity')} {_q('DEPLOYER_SERVER_TLS_CERT_FILE=' + manifest['tls']['cert_path'])} {_q('DEPLOYER_SERVER_TLS_KEY_FILE=' + manifest['tls']['key_path'])} >> /etc/deployer/server.env",
        f"set -euo pipefail; umask 077; printf '%s' {_q(config_b64)} | base64 -d > /etc/rancher/k3s/config.yaml; k3swork=$(mktemp -d /tmp/deployer-k3s.XXXXXX); trap 'rm -rf \"$k3swork\"' EXIT; curl -fsSL {_q(k3s['installer_url'])} -o \"$k3swork/install.sh\"; echo {_q(k3s['installer_sha256'] + '  ')}\"$k3swork/install.sh\" | sha256sum -c -; curl -fsSL {_q('https://github.com/k3s-io/k3s/releases/download/' + k3s['version'] + ('/k3s-arm64' if 'arm64' in asset else '/k3s'))} -o \"$k3swork/k3s\"; echo {_q(k3s['binary_sha256'] + '  ')}\"$k3swork/k3s\" | sha256sum -c -; install -m 0755 \"$k3swork/k3s\" /usr/local/bin/k3s; INSTALL_K3S_SKIP_DOWNLOAD=true INSTALL_K3S_EXEC=server sh \"$k3swork/install.sh\"; for attempt in $(seq 1 60); do if /usr/local/bin/k3s kubectl get nodes -o name --request-timeout=5s | grep -q '^node/'; then break; fi; sleep 2; done; /usr/local/bin/k3s kubectl wait --for=condition=Ready nodes --all --timeout=180s",
        f"/usr/local/bin/k3s kubectl create namespace {_q(namespace)} && printf '%s' {_q(quota_b64)} | base64 -d | /usr/local/bin/k3s kubectl apply -f -",
        "set -euo pipefail; systemctl daemon-reload; systemctl enable --now deployer-server.service; curl --fail --retry 20 --retry-connrefused --retry-delay 2 http://127.0.0.1:7080/readyz",
        f"openssl s_client -connect {_q('127.0.0.1:' + str(urlsplit(manifest['admin_endpoint']).port))} -servername {_q(urlsplit(manifest['admin_endpoint']).hostname)} -verify_hostname {_q(urlsplit(manifest['admin_endpoint']).hostname)} -verify_return_error < /dev/null",
        f"set -euo pipefail; umask 077; install -d -m 700 /etc/deployer/backup /var/lib/deployer/backups; [ -e /etc/deployer/backup/key ] || openssl rand -out /etc/deployer/backup/key 32; deployer-server backup create --database-path /var/lib/deployer/deployer.db --output /var/lib/deployer/backups/platform-initial.enc --key-file /etc/deployer/backup/key; printf '%s' {_q(json.dumps({'environment_id': e['id'], 'customer_id': manifest['customer']['id'], 'wireguard_interface': n.get('wireguard_interface', 'wg0'), 'recovery_targets': manifest['recovery']['targets']}))} > /etc/deployer/identity.json; chmod 600 /etc/deployer/identity.json",
    ]

def redact(text: str) -> str:
    text = TOKEN.sub("<redacted-token>", text)
    return SECRET_WORDS.sub(lambda m: m.group(0).split("=", 1)[0] + "=<redacted>", text)

def run_provision(manifest: dict[str, Any], runner: Runner, output_dir: Path, *, apply: bool) -> dict[str, Any]:
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    if output_dir.is_symlink() or output_dir.stat().st_uid != os.geteuid() or output_dir.stat().st_mode & 0o077:
        raise ProvisioningError("output directory must be private and owned")
    validate_manifest(manifest)
    inventory = runner.run(READ_ONLY_INVENTORY)
    refuse_existing_node(inventory)
    tls = manifest["tls"]
    cert, key = _q(tls['cert_path']), _q(tls['key_path'])
    hostname = _q(urlsplit(manifest['admin_endpoint']).hostname)
    runner.run(f"set -euo pipefail; test -f {cert}; test -f {key}; test ! -L {key}; test \"$(stat -c %a {key})\" = 600; test \"$(stat -c %u {key})\" = 0; openssl x509 -in {cert} -noout -checkend 86400; openssl x509 -in {cert} -noout -checkhost {hostname}; test \"$(openssl x509 -in {cert} -pubkey -noout)\" = \"$(openssl pkey -in {key} -pubout)\"")
    plan = build_plan(manifest)
    result = {
        "environment_id": manifest["environment"]["id"],
        "customer_id": manifest["customer"]["id"],
        "release": manifest["release"]["version"],
        "admin_endpoint": manifest["admin_endpoint"],
        "plan": plan,
        "inventory_sha256": hashlib.sha256(inventory.encode()).hexdigest(),
        "credentials": {
            "admin_token": "emitted once by bootstrap; store in an operator secret manager",
            "cli_context": f"deployer --context {manifest['environment']['id']} login {manifest['admin_endpoint']}",
        },
        "qualification_pending": ["worker onboarding", "application ingress TLS", "offsite backup schedule", "restore drill", "external alerts", "customer data backup coverage"],
    }
    if apply:
        bootstrap_output = []
        result["status"] = "applying"
        result["completed_steps"] = 0
        for index, command in enumerate(plan[1:], 1):
            try:
                output = runner.run(command, capture=True)
            except Exception:
                result["status"] = "failed"
                result["failed_step"] = index
                _private_write(output_dir / f"{manifest['environment']['id']}-install-record.json", json.dumps(result, indent=2) + "\n")
                raise
            result["completed_steps"] = index
            _private_write(output_dir / f"{manifest['environment']['id']}-install-record.json", json.dumps(result, indent=2) + "\n")
            if "bootstrap server" in command:
                bootstrap_output.append(output)
                _private_write(output_dir / f"{manifest['environment']['id']}-bootstrap-output.txt", output)
        credential_file = output_dir / f"{manifest['environment']['id']}-bootstrap-output.txt"
        _private_write(credential_file, "\n".join(bootstrap_output))
        result["credential_output"] = str(credential_file)
        result["status"] = "applied"
    else:
        result["status"] = "planned"
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    if output_dir.stat().st_mode & 0o077:
        raise ProvisioningError("output directory must not be accessible by other users")
    record = output_dir / f"{manifest['environment']['id']}-install-record.json"
    _private_write(record, json.dumps(result, indent=2, sort_keys=True) + "\n")
    return result


def _private_write(path: Path, content: str) -> None:
    temporary = path.with_name("." + path.name + ".tmp")
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    fd = os.open(temporary, flags, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except Exception:
        try:
            os.close(fd)
        except OSError:
            pass
        try:
            temporary.unlink()
        except OSError:
            pass
        raise

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("action", choices=("plan", "apply"), nargs="?", default="plan")
    parser.add_argument("--apply", action="store_true", help="required confirmation for the apply action")
    parser.add_argument("--registry", type=Path, required=True, help="JSON registry of existing environment CIDRs")
    parser.add_argument("--output-dir", type=Path, default=Path("provisioning-records"))
    args = parser.parse_args(argv)
    try:
        with args.registry.with_suffix(args.registry.suffix + '.lock').open('a') as lock:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
            return execute(args)
    except (OSError, ValueError, KeyError, TypeError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

def execute(args):
    manifest = _load_data(args.manifest)
    raw_registry = _load_data(args.registry) if args.registry else None
    registry = raw_registry
    if isinstance(registry, dict):
        registry = registry.get("environments", [])
    if not isinstance(registry, list) or any(not isinstance(item, dict) for item in registry):
        raise ProvisioningError("registry must be a list of environment records")
    for item in registry:
        if not item.get('environment_id', item.get('id')) or any(not item.get(key) for key in ('wireguard_cidr', 'pod_cidr', 'service_cidr')):
            raise ProvisioningError("every registry entry requires an identity and three network CIDRs")
    validate_manifest(manifest, registry)
    runner = SSHRunner(host=manifest["ssh"]["host"], user=manifest["ssh"]["user"], port=int(manifest["ssh"].get("port", 22)), identity_file=manifest["ssh"].get("identity_file"))
    if args.action == "apply" and not args.apply:
        raise ProvisioningError("apply requires explicit --apply confirmation")
    result = run_provision(manifest, runner, args.output_dir, apply=args.action == "apply")
    if args.action == "apply":
        registry.append({"environment_id": manifest["environment"]["id"], "customer_id": manifest["customer"]["id"], "ssh_host": manifest["ssh"]["host"], "release": manifest["release"]["version"], **{key: manifest["network"][key] for key in ("wireguard_cidr", "pod_cidr", "service_cidr")}})
        _private_write(args.registry, json.dumps(dict(raw_registry, environments=registry) if isinstance(raw_registry, dict) else registry, indent=2) + "\n")
    print(json.dumps({"status": result["status"], "record": str(args.output_dir / (manifest['environment']['id'] + '-install-record.json')), "plan": result["plan"]}, indent=2))
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
