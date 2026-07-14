#!/bin/sh
set -eu

usage() {
  cat >&2 <<'USAGE'
Usage: install-release.sh [--repo OWNER/REPO] [--version VERSION|latest] [--role auto|server|agent|cli|all] [--no-restart] [--no-enable-timer]

Downloads a GitHub release artifact for the current Linux architecture,
verifies it against the release checksums.txt, installs binaries into
/usr/local/bin, and restarts the matching systemd service when requested.

Options:
  --repo OWNER/REPO        GitHub repository (default: 0xivanov/self-hosted-deployer)
  --version VERSION        Release tag, for example v0.1.0 (default: latest)
  --role ROLE              auto, server, agent, cli, or all (default: auto)
  --install-dir DIR        CLI-only install directory (default: /usr/local/bin)
  --no-restart             Install files without restarting systemd services
  --no-enable-timer        Do not enable or start the automatic-update timer
USAGE
}

REPO="0xivanov/self-hosted-deployer"
VERSION="latest"
ROLE="auto"
INSTALL_DIR="/usr/local/bin"
RESTART="1"
ENABLE_TIMER="1"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --repo)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--repo requires OWNER/REPO" >&2
        usage
        exit 2
      fi
      REPO="$2"
      shift 2
      ;;
    --version)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--version requires a release tag or latest" >&2
        usage
        exit 2
      fi
      VERSION="$2"
      shift 2
      ;;
    --role)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--role requires auto, server, agent, cli, or all" >&2
        usage
        exit 2
      fi
      ROLE="$2"
      shift 2
      ;;
    --install-dir)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--install-dir requires a directory" >&2
        usage
        exit 2
      fi
      INSTALL_DIR="$2"
      shift 2
      ;;
    --no-restart)
      RESTART="0"
      shift
      ;;
    --no-enable-timer)
      ENABLE_TIMER="0"
      shift
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

if [ "$(id -u)" != "0" ]; then
  echo "install-release.sh must run as root; rerun with sudo" >&2
  exit 1
fi

if ! command -v flock >/dev/null 2>&1; then
  echo "flock is required to serialize deployer updates" >&2
  exit 1
fi
UPDATE_LOCK="/run/deployer-auto-update.lock"
inherited_update_lock="0"
if command -v readlink >/dev/null 2>&1 && [ -e /proc/self/fd/9 ]; then
  inherited_fd=$(readlink /proc/self/fd/9 2>/dev/null || true)
  if [ "$inherited_fd" = "$UPDATE_LOCK" ] && flock -n 9; then
    inherited_update_lock="1"
  fi
fi
if [ "$inherited_update_lock" != "1" ]; then
  exec 9>"$UPDATE_LOCK"
  flock 9
fi

if [ "$(uname -s)" != "Linux" ]; then
  echo "only Linux release artifacts are supported by this installer" >&2
  exit 1
fi

case "$ROLE" in
  auto|server|agent|cli|all)
    ;;
  *)
    echo "unsupported role: $ROLE" >&2
    usage
    exit 2
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64)
    PLATFORM="linux-amd64"
    ;;
  aarch64|arm64)
    PLATFORM="linux-arm64"
    ;;
  *)
    echo "unsupported machine architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

if command -v curl >/dev/null 2>&1; then
  download() {
    curl -fsSL "$1" -o "$2"
  }
elif command -v wget >/dev/null 2>&1; then
  download() {
    wget -qO "$2" "$1"
  }
else
  echo "curl or wget is required" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  check_sha256() {
    sha256sum -c "$1"
  }
elif command -v shasum >/dev/null 2>&1; then
  check_sha256() {
    shasum -a 256 -c "$1"
  }
else
  echo "sha256sum or shasum is required" >&2
  exit 1
fi

if [ "$VERSION" = "latest" ]; then
  RELEASE_PATH="latest/download"
else
  RELEASE_PATH="download/$VERSION"
fi

ASSET="deployer-$PLATFORM.tar.gz"
BASE_URL="https://github.com/$REPO/releases/$RELEASE_PATH"
WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/deployer-release.XXXXXX")
cleanup() {
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

echo "Downloading $ASSET from $REPO ($VERSION)"
download "$BASE_URL/$ASSET" "$WORKDIR/$ASSET"
download "$BASE_URL/checksums.txt" "$WORKDIR/checksums.txt"

(
  cd "$WORKDIR"
  grep "  $ASSET\$" checksums.txt > "$ASSET.sha256"
  check_sha256 "$ASSET.sha256"
  tar -xzf "$ASSET"
)

PACKAGE_DIR="$WORKDIR/deployer-$PLATFORM"
if [ ! -d "$PACKAGE_DIR" ]; then
  echo "release archive did not contain $PACKAGE_DIR" >&2
  exit 1
fi

has_unit() {
  if ! command -v systemctl >/dev/null 2>&1; then
    return 1
  fi
  systemctl list-unit-files "$1" --no-legend 2>/dev/null | grep -q "^$1[[:space:]]" && return 0
  systemctl status "$1" >/dev/null 2>&1
}

install_cli() {
  install -d -m 0755 "$INSTALL_DIR"
  install -m 0755 "$PACKAGE_DIR/deployer" "$INSTALL_DIR/deployer"
}

install_server() {
  install_cli
  install -d -m 0755 /usr/local/sbin
  install -m 0755 "$PACKAGE_DIR/deployer-server" "$INSTALL_DIR/deployer-server"
  install -m 0755 "$PACKAGE_DIR/scripts/auto-update.sh" /usr/local/sbin/deployer-auto-update
  install -m 0755 "$PACKAGE_DIR/scripts/install-release.sh" /usr/local/sbin/deployer-install-release.new
  mv -f /usr/local/sbin/deployer-install-release.new /usr/local/sbin/deployer-install-release
  install -m 0644 "$PACKAGE_DIR/deploy/systemd/deployer-server.service" /etc/systemd/system/deployer-server.service
  install -m 0644 "$PACKAGE_DIR/deploy/systemd/deployer-auto-update-server.service" /etc/systemd/system/deployer-auto-update-server.service
  install -m 0644 "$PACKAGE_DIR/deploy/systemd/deployer-auto-update-server.timer" /etc/systemd/system/deployer-auto-update-server.timer
}

install_agent() {
  install -d -m 0755 "$INSTALL_DIR"
  install -d -m 0755 /usr/local/sbin
  install -m 0755 "$PACKAGE_DIR/deployer-agent" "$INSTALL_DIR/deployer-agent"
  install -m 0755 "$PACKAGE_DIR/scripts/auto-update.sh" /usr/local/sbin/deployer-auto-update
  install -m 0755 "$PACKAGE_DIR/scripts/install-release.sh" /usr/local/sbin/deployer-install-release.new
  mv -f /usr/local/sbin/deployer-install-release.new /usr/local/sbin/deployer-install-release
  install -m 0644 "$PACKAGE_DIR/deploy/systemd/deployer-agent.service" /etc/systemd/system/deployer-agent.service
  install -m 0644 "$PACKAGE_DIR/deploy/systemd/deployer-auto-update-agent.service" /etc/systemd/system/deployer-auto-update-agent.service
  install -m 0644 "$PACKAGE_DIR/deploy/systemd/deployer-auto-update-agent.timer" /etc/systemd/system/deployer-auto-update-agent.timer
}

if [ "$ROLE" = "auto" ]; then
  ROLE="cli"
  if has_unit deployer-server.service; then
    ROLE="server"
  elif has_unit deployer-agent.service; then
    ROLE="agent"
  fi
fi

if [ "$INSTALL_DIR" != "/usr/local/bin" ]; then
  case "$ROLE" in
    server|agent|all)
      echo "--install-dir is only supported for the cli role; system services use /usr/local/bin" >&2
      exit 2
      ;;
  esac
fi

case "$ROLE" in
  cli)
    install_cli
    ;;
  server)
    install_server
    ;;
  agent)
    install_agent
    ;;
  all)
    install_server
    install_agent
    ;;
esac

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
  if [ "$ENABLE_TIMER" = "1" ]; then
    case "$ROLE" in
      server)
        systemctl enable --now deployer-auto-update-server.timer
        ;;
      agent)
        systemctl enable --now deployer-auto-update-agent.timer
        ;;
      all)
        systemctl enable --now deployer-auto-update-server.timer deployer-auto-update-agent.timer
        ;;
    esac
  fi
  if [ "$RESTART" = "1" ]; then
    case "$ROLE" in
      server)
        systemctl restart deployer-server.service
        ;;
      agent)
        systemctl restart deployer-agent.service
        ;;
      all)
        systemctl restart deployer-server.service || true
        systemctl restart deployer-agent.service || true
        ;;
    esac
  fi
fi

echo "Installed $REPO $VERSION ($PLATFORM) for role $ROLE"
