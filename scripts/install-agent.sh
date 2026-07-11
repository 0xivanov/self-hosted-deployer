#!/bin/sh
set -eu

usage() {
  cat >&2 <<'USAGE'
Usage: install-agent.sh --server URL --token JOIN_TOKEN [--agent-binary PATH]

Options:
  --server URL          Control plane gRPC URL, for example https://deploy.example.com:7443
  --token TOKEN         One-time node join token from deployer nodes add
  --agent-binary PATH   Path to deployer-agent binary (default: ./deployer-agent or PATH)
USAGE
}

SERVER_URL=""
JOIN_TOKEN=""
AGENT_BINARY=""
BIN_DIR="/usr/local/bin"
ENV_FILE="/etc/deployer/agent.env"
SERVICE_TEMPLATE="./deploy/systemd/deployer-agent.service"
SERVICE_FILE="/etc/systemd/system/deployer-agent.service"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --server)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--server requires a URL" >&2
        usage
        exit 2
      fi
      SERVER_URL="${2:-}"
      shift 2
      ;;
    --token)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--token requires a join token" >&2
        usage
        exit 2
      fi
      JOIN_TOKEN="${2:-}"
      shift 2
      ;;
    --agent-binary)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--agent-binary requires a path" >&2
        usage
        exit 2
      fi
      AGENT_BINARY="${2:-}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [ -z "$SERVER_URL" ] || [ -z "$JOIN_TOKEN" ]; then
  usage
  exit 2
fi

if [ "$(id -u)" != "0" ]; then
  echo "install-agent.sh must run as root; rerun with sudo" >&2
  exit 1
fi

if [ "$(uname -s)" != "Linux" ]; then
  echo "only Linux agents are supported" >&2
  exit 1
fi

case "$(uname -m)" in
  aarch64|arm64|x86_64|amd64)
    ;;
  *)
    echo "unsupported machine architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemctl is required to install deployer-agent.service" >&2
  exit 1
fi

memory_cgroup_enabled() {
  if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    grep -qw memory /sys/fs/cgroup/cgroup.controllers
    return $?
  fi
  [ -d /sys/fs/cgroup/memory ]
}

ensure_memory_cgroup() {
  if memory_cgroup_enabled; then
    return 0
  fi

  CMDLINE_FILE=""
  for candidate in /boot/firmware/cmdline.txt /boot/cmdline.txt; do
    if [ -f "$candidate" ]; then
      CMDLINE_FILE="$candidate"
      break
    fi
  done

  if [ -z "$CMDLINE_FILE" ]; then
    echo "memory cgroup is not enabled; add 'cgroup_memory=1 cgroup_enable=memory' to the kernel cmdline, reboot, and rerun this installer" >&2
    exit 1
  fi

  current_cmdline=$(cat "$CMDLINE_FILE")
  changed=0
  case " $current_cmdline " in
    *" cgroup_memory=1 "*) ;;
    *)
      current_cmdline="$current_cmdline cgroup_memory=1"
      changed=1
      ;;
  esac
  case " $current_cmdline " in
    *" cgroup_enable=memory "*) ;;
    *)
      current_cmdline="$current_cmdline cgroup_enable=memory"
      changed=1
      ;;
  esac

  if [ "$changed" = "1" ]; then
    printf '%s\n' "$current_cmdline" > "$CMDLINE_FILE"
    echo "Enabled memory cgroup boot flags in $CMDLINE_FILE."
  fi
  echo "Reboot this node, then rerun this installer with the same unexpired join token or a fresh one." >&2
  exit 1
}

if [ -z "$AGENT_BINARY" ]; then
  if [ -x "./deployer-agent" ]; then
    AGENT_BINARY="./deployer-agent"
  elif command -v deployer-agent >/dev/null 2>&1; then
    AGENT_BINARY="$(command -v deployer-agent)"
  else
    echo "deployer-agent binary not found; pass --agent-binary PATH" >&2
    exit 1
  fi
fi
if [ ! -x "$AGENT_BINARY" ]; then
  echo "agent binary is not executable: $AGENT_BINARY" >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
if [ ! -f "$SERVICE_TEMPLATE" ] && [ -f "$SCRIPT_DIR/../deploy/systemd/deployer-agent.service" ]; then
  SERVICE_TEMPLATE="$SCRIPT_DIR/../deploy/systemd/deployer-agent.service"
fi
if [ ! -f "$SERVICE_TEMPLATE" ]; then
  echo "deployer-agent systemd template not found at $SERVICE_TEMPLATE" >&2
  exit 1
fi

install -d -m 0755 "$BIN_DIR"
install -m 0755 "$AGENT_BINARY" "$BIN_DIR/deployer-agent"
install -d -m 0700 /etc/deployer/agent /etc/deployer/wireguard /etc/wireguard
cat > "$ENV_FILE" <<EOF
DEPLOYER_SERVER_URL=$SERVER_URL
DEPLOYER_AGENT_CREDENTIAL_PATH=/etc/deployer/agent/token
DEPLOYER_WIREGUARD_INTERFACE=wg0
DEPLOYER_WIREGUARD_PRIVATE_KEY_PATH=/etc/deployer/wireguard/privatekey
DEPLOYER_WIREGUARD_CONFIG_PATH=/etc/wireguard/wg0.conf
DEPLOYER_WIREGUARD_HUB_IP=10.8.0.1
DEPLOYER_K3S_CONFIG_PATH=/etc/rancher/k3s/config.yaml
DEPLOYER_K3S_INSTALLER_URL=https://get.k3s.io
EOF
chmod 0600 "$ENV_FILE"

ensure_memory_cgroup
"$BIN_DIR/deployer-agent" join --server "$SERVER_URL" --token "$JOIN_TOKEN"
"$BIN_DIR/deployer-agent" join-k3s --server "$SERVER_URL"

install -m 0644 "$SERVICE_TEMPLATE" "$SERVICE_FILE"
systemctl daemon-reload
systemctl enable --now deployer-agent.service

echo "deployer-agent installed and started"
