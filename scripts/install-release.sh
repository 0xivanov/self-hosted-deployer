#!/bin/sh
set -eu

usage() {
  cat >&2 <<'USAGE'
Usage: install-release.sh [--repo OWNER/REPO] [--version VERSION|latest] [--role auto|server|agent|cli|all] [--update-policy manual|pinned|latest] [--policy-file FILE] [--no-restart] [--no-enable-timer]

Downloads a GitHub release artifact for the current Linux architecture,
verifies it against the release checksums.txt, installs binaries into
/usr/local/bin, and restarts the matching systemd service when requested.

Options:
  --repo OWNER/REPO        GitHub repository (default: 0xivanov/self-hosted-deployer)
  --version VERSION        Release tag, for example v0.1.0 (default: latest)
  --role ROLE              auto, server, agent, cli, or all (default: auto)
  --update-policy MODE     Persist manual, pinned, or latest update policy
  --policy-file FILE       Policy path (default: /etc/deployer/update-policy.conf)
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
POLICY_FILE=${DEPLOYER_UPDATE_POLICY_FILE:-/etc/deployer/update-policy.conf}
REQUESTED_POLICY=""

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
    --update-policy)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--update-policy requires manual, pinned, or latest" >&2
        usage
        exit 2
      fi
      REQUESTED_POLICY="$2"
      shift 2
      ;;
    --policy-file)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--policy-file requires a path" >&2
        usage
        exit 2
      fi
      POLICY_FILE="$2"
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

policy_mode="latest"
policy_version=""
policy_present="0"
read_policy() {
  if [ -e "$POLICY_FILE" ] || [ -L "$POLICY_FILE" ]; then
    [ -f "$POLICY_FILE" ] || { echo "invalid update policy: path is not a regular file" >&2; return 1; }
  else
    return 0
  fi
  policy_present="1"
  policy_mode=""
  policy_version=""
  seen_mode="0"
  seen_version="0"
  while IFS='=' read -r key value || [ -n "$key" ]; do
    case "$key" in
      ''|'#'*) continue ;;
      mode) [ "$seen_mode" = "0" ] || { echo "invalid update policy: duplicate mode" >&2; return 1; }; policy_mode=$value; seen_mode="1" ;;
      version) [ "$seen_version" = "0" ] || { echo "invalid update policy: duplicate version" >&2; return 1; }; policy_version=$value; seen_version="1" ;;
      *) echo "invalid update policy: unknown key $key" >&2; return 1 ;;
    esac
  done <"$POLICY_FILE"
  case "$policy_mode" in
    manual|latest) [ -z "$policy_version" ] || { echo "invalid update policy: version is only valid for pinned mode" >&2; return 1; } ;;
    pinned) [ -n "$policy_version" ] || { echo "invalid update policy: pinned mode requires version" >&2; return 1; } ;;
    *) echo "invalid update policy: mode must be manual, pinned, or latest" >&2; return 1 ;;
  esac
}

semver_parts() {
  # Bound numeric components before shell arithmetic; require one exact tag.
  case "$1" in *'
'*) return 1 ;; esac
  printf '%s\n' "$1" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || return 1
  version=${1#v}
  core=${version%%-*}
  prerelease=""
  if [ "$core" != "$version" ]; then
    prerelease=${version#*-}
    case "$prerelease" in ''|*[!0-9A-Za-z.-]*) return 1 ;; esac
    old_ifs=$IFS
    IFS=.
    for identifier in $prerelease; do
      [ -n "$identifier" ] || { IFS=$old_ifs; return 1; }
    done
    IFS=$old_ifs
  fi
  major=${core%%.*}
  remainder=${core#*.}
  [ "$remainder" != "$core" ] || return 1
  minor=${remainder%%.*}
  patch=${remainder#*.}
  [ "$patch" != "$remainder" ] || return 1
  for component in "$major" "$minor" "$patch"; do
    case "$component" in ''|*[!0-9]*) return 1 ;; esac
  done
  printf '%s:%s:%s:%s\n' "$major" "$minor" "$patch" "${prerelease:-_release}"
}
read_policy || exit 1
if [ -n "$REQUESTED_POLICY" ]; then
  case "$REQUESTED_POLICY" in manual|pinned|latest) ;; *) echo "invalid update policy: $REQUESTED_POLICY" >&2; exit 2 ;; esac
  policy_mode="$REQUESTED_POLICY"
  policy_present="1"
  if [ "$policy_mode" = "pinned" ]; then
    case "$VERSION" in v[0-9]*.[0-9]*.[0-9]*) policy_version="$VERSION" ;; *) echo "pinned policy requires --version vMAJOR.MINOR.PATCH" >&2; exit 2 ;; esac
  else
    policy_version=""
  fi
fi
if [ "$policy_mode" = "pinned" ]; then
  if [ "$VERSION" = "latest" ]; then VERSION="$policy_version"; fi
  [ "$VERSION" = "$policy_version" ] || { echo "requested version $VERSION does not match pinned policy $policy_version" >&2; exit 1; }
  semver_parts "$policy_version" >/dev/null 2>&1 || { echo "invalid pinned release version: $policy_version" >&2; exit 1; }
fi

if ! command -v flock >/dev/null 2>&1; then
  echo "flock is required to serialize deployer updates" >&2
  exit 1
fi
UPDATE_LOCK=${DEPLOYER_UPDATE_LOCK:-/run/deployer-auto-update.lock}
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
preserve_workdir="0"
mutations_started="0"
cleanup() {
  if [ "$preserve_workdir" = "1" ]; then
    echo "installer rollback snapshot preserved at $WORKDIR" >&2
  else
    rm -rf "$WORKDIR"
  fi
}
on_signal() {
  signal_status=$1
  trap '' INT TERM
  set +e
  if [ "$mutations_started" = "1" ]; then
    rollback_transaction
  fi
  exit "$signal_status"
}
trap cleanup EXIT
trap 'on_signal 130' INT
trap 'on_signal 143' TERM

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
  if [ "${server_timer_masked:-0}" != "1" ]; then
    install -m 0644 "$PACKAGE_DIR/deploy/systemd/deployer-auto-update-server.timer" /etc/systemd/system/deployer-auto-update-server.timer
  fi
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
  if [ "${agent_timer_masked:-0}" != "1" ]; then
    install -m 0644 "$PACKAGE_DIR/deploy/systemd/deployer-auto-update-agent.timer" /etc/systemd/system/deployer-auto-update-agent.timer
  fi
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

timer_state="absent"
timer_unit_for_role=""
server_timer_state="absent"
agent_timer_state="absent"
server_timer_active="inactive"
agent_timer_active="inactive"
server_timer_masked="0"
agent_timer_masked="0"
server_timer_exists="0"
agent_timer_exists="0"
server_service_active="inactive"
agent_service_active="inactive"
server_service_exists="0"
agent_service_exists="0"
case "$ROLE" in
  server) timer_unit_for_role=deployer-auto-update-server.timer ;;
  agent) timer_unit_for_role=deployer-auto-update-agent.timer ;;
  all) timer_unit_for_role=deployer-auto-update-server.timer ;;
esac
if command -v systemctl >/dev/null 2>&1 && [ -n "$timer_unit_for_role" ]; then
  timer_probe=$(systemctl is-enabled "$timer_unit_for_role" 2>/dev/null || true)
  case "$ROLE" in
    agent)
      if [ -n "$timer_probe" ]; then agent_timer_exists="1"; fi
      case "$timer_probe" in
        enabled) timer_state="enabled"; agent_timer_state="enabled" ;;
        masked|masked-runtime) timer_state="disabled"; agent_timer_state="disabled"; agent_timer_masked="1" ;;
        disabled|static|indirect|enabled-runtime) timer_state="disabled"; agent_timer_state="disabled" ;;
      esac
      if systemctl is-active --quiet "$timer_unit_for_role" 2>/dev/null; then agent_timer_active="active"; fi
      ;;
    *)
      if [ -n "$timer_probe" ]; then server_timer_exists="1"; fi
      case "$timer_probe" in
        enabled) timer_state="enabled"; server_timer_state="enabled" ;;
        masked|masked-runtime) timer_state="disabled"; server_timer_state="disabled"; server_timer_masked="1" ;;
        disabled|static|indirect|enabled-runtime) timer_state="disabled"; server_timer_state="disabled" ;;
      esac
      if systemctl is-active --quiet "$timer_unit_for_role" 2>/dev/null; then server_timer_active="active"; fi
      ;;
  esac
fi
if [ "$ROLE" = "all" ] && command -v systemctl >/dev/null 2>&1; then
  agent_timer_probe=$(systemctl is-enabled deployer-auto-update-agent.timer 2>/dev/null || true)
  if [ -n "$agent_timer_probe" ]; then agent_timer_exists="1"; fi
  case "$agent_timer_probe" in
    enabled) agent_timer_state="enabled" ;;
    masked|masked-runtime) agent_timer_state="disabled"; agent_timer_masked="1" ;;
    disabled|static|indirect|enabled-runtime) agent_timer_state="disabled" ;;
  esac
  if systemctl is-active --quiet deployer-auto-update-agent.timer 2>/dev/null; then agent_timer_active="active"; fi
fi

if command -v systemctl >/dev/null 2>&1; then
  if [ -e /etc/systemd/system/deployer-server.service ] || [ -L /etc/systemd/system/deployer-server.service ]; then server_service_exists="1"; fi
  if [ -e /etc/systemd/system/deployer-agent.service ] || [ -L /etc/systemd/system/deployer-agent.service ]; then agent_service_exists="1"; fi
  case "$ROLE" in
    server) systemctl is-active --quiet deployer-server.service 2>/dev/null && server_service_active="active" || true ;;
    agent) systemctl is-active --quiet deployer-agent.service 2>/dev/null && agent_service_active="active" || true ;;
    all)
      systemctl is-active --quiet deployer-server.service 2>/dev/null && server_service_active="active" || true
      systemctl is-active --quiet deployer-agent.service 2>/dev/null && agent_service_active="active" || true
      ;;
  esac
fi

write_policy() {
  [ "$policy_present" = "1" ] || return 0
  policy_dir=${POLICY_FILE%/*}
  [ "$policy_dir" != "$POLICY_FILE" ] || policy_dir=.
  if [ ! -d "$policy_dir" ]; then install -d -m 0755 "$policy_dir"; fi
  policy_tmp="$POLICY_FILE.tmp.$$"
  {
    printf 'mode=%s\n' "$policy_mode"
    if [ "$policy_mode" = "pinned" ]; then printf 'version=%s\n' "$policy_version"; fi
  } >"$policy_tmp"
  chmod 0644 "$policy_tmp"
  mv -f "$policy_tmp" "$POLICY_FILE"
}

rollback_dir="$WORKDIR/installer-rollback"
mkdir -p "$rollback_dir"
snapshot_item() {
  source_path=$1
  snapshot_name=$2
  if [ -L "$source_path" ]; then
    readlink "$source_path" >"$rollback_dir/$snapshot_name.link"
  elif [ -f "$source_path" ]; then
    cp -p "$source_path" "$rollback_dir/$snapshot_name.file"
  else
    : >"$rollback_dir/$snapshot_name.absent"
  fi
}
restore_item() {
  target_path=$1
  snapshot_name=$2
  if [ -f "$rollback_dir/$snapshot_name.absent" ]; then
    rm -f "$target_path"
    return
  fi
  if [ -f "$rollback_dir/$snapshot_name.link" ]; then
    temporary_path="$target_path.deployer-rollback-link.$$"
    rm -f "$temporary_path"
    ln -s "$(cat "$rollback_dir/$snapshot_name.link")" "$temporary_path" || return
    rm -f "$target_path" || { rm -f "$temporary_path"; return 1; }
    mv -f "$temporary_path" "$target_path"
    return
  fi
  temporary_path="$target_path.deployer-rollback.$$"
  rm -f "$temporary_path"
  cp -p "$rollback_dir/$snapshot_name.file" "$temporary_path" || return
  rm -f "$target_path" || { rm -f "$temporary_path"; return 1; }
  mv -f "$temporary_path" "$target_path"
}
snapshot_files() {
  snapshot_item "$INSTALL_DIR/deployer" cli
  case "$ROLE" in server|all) snapshot_item "$INSTALL_DIR/deployer-server" server-binary ;; esac
  case "$ROLE" in agent|all) snapshot_item "$INSTALL_DIR/deployer-agent" agent-binary ;; esac
  case "$ROLE" in server|agent|all)
    snapshot_item /usr/local/sbin/deployer-auto-update updater
    snapshot_item /usr/local/sbin/deployer-install-release installer
    ;;
  esac
  case "$ROLE" in server|all)
    snapshot_item /etc/systemd/system/deployer-server.service server-unit
    snapshot_item /etc/systemd/system/deployer-auto-update-server.service server-updater-unit
    snapshot_item /etc/systemd/system/deployer-auto-update-server.timer server-timer-unit
    ;;
  esac
  case "$ROLE" in agent|all)
    snapshot_item /etc/systemd/system/deployer-agent.service agent-unit
    snapshot_item /etc/systemd/system/deployer-auto-update-agent.service agent-updater-unit
    snapshot_item /etc/systemd/system/deployer-auto-update-agent.timer agent-timer-unit
    ;;
  esac
  snapshot_item "$POLICY_FILE" policy
}
restore_files() {
  rollback_item_status=0
  restore_checked() { restore_item "$@" || rollback_item_status=1; }
  restore_checked "$INSTALL_DIR/deployer" cli
  case "$ROLE" in server|all) restore_checked "$INSTALL_DIR/deployer-server" server-binary ;; esac
  case "$ROLE" in agent|all) restore_checked "$INSTALL_DIR/deployer-agent" agent-binary ;; esac
  case "$ROLE" in server|agent|all)
    restore_checked /usr/local/sbin/deployer-auto-update updater
    restore_checked /usr/local/sbin/deployer-install-release installer
    ;;
  esac
  case "$ROLE" in server|all)
    restore_checked /etc/systemd/system/deployer-server.service server-unit
    restore_checked /etc/systemd/system/deployer-auto-update-server.service server-updater-unit
    restore_checked /etc/systemd/system/deployer-auto-update-server.timer server-timer-unit
    ;;
  esac
  case "$ROLE" in agent|all)
    restore_checked /etc/systemd/system/deployer-agent.service agent-unit
    restore_checked /etc/systemd/system/deployer-auto-update-agent.service agent-updater-unit
    restore_checked /etc/systemd/system/deployer-auto-update-agent.timer agent-timer-unit
    ;;
  esac
  restore_checked "$POLICY_FILE" policy
  return "$rollback_item_status"
}
restore_runtime_state() {
  runtime_status=0
  runtime_checked() { "$@" || runtime_status=1; }
  if [ "$RESTART" != "1" ] && [ "$ENABLE_TIMER" != "1" ]; then return 0; fi
  case "$ROLE" in
    server|all)
      if [ "$ENABLE_TIMER" = "1" ] && [ "$server_timer_exists" = "1" ] && [ "$server_timer_masked" = "0" ]; then
        if [ "$server_timer_state" = "enabled" ]; then
          if [ "$server_timer_active" = "active" ]; then runtime_checked systemctl enable --now deployer-auto-update-server.timer
          else runtime_checked systemctl disable --now deployer-auto-update-server.timer; runtime_checked systemctl enable deployer-auto-update-server.timer
          fi
        elif [ "$server_timer_active" = "active" ]; then
          runtime_checked systemctl disable deployer-auto-update-server.timer; runtime_checked systemctl start deployer-auto-update-server.timer
        else runtime_checked systemctl disable --now deployer-auto-update-server.timer
        fi
      fi
      if [ "$RESTART" = "1" ] && [ "$server_service_exists" = "1" ]; then
        if [ "$server_service_active" = "active" ]; then runtime_checked systemctl restart deployer-server.service; else runtime_checked systemctl stop deployer-server.service; fi
      fi
      ;;
  esac
  case "$ROLE" in
    agent|all)
      if [ "$ENABLE_TIMER" = "1" ] && [ "$agent_timer_exists" = "1" ] && [ "$agent_timer_masked" = "0" ]; then
        if [ "$agent_timer_state" = "enabled" ]; then
          if [ "$agent_timer_active" = "active" ]; then runtime_checked systemctl enable --now deployer-auto-update-agent.timer
          else runtime_checked systemctl disable --now deployer-auto-update-agent.timer; runtime_checked systemctl enable deployer-auto-update-agent.timer
          fi
        elif [ "$agent_timer_active" = "active" ]; then
          runtime_checked systemctl disable deployer-auto-update-agent.timer; runtime_checked systemctl start deployer-auto-update-agent.timer
        else runtime_checked systemctl disable --now deployer-auto-update-agent.timer
        fi
      fi
      if [ "$RESTART" = "1" ] && [ "$agent_service_exists" = "1" ]; then
        if [ "$agent_service_active" = "active" ]; then runtime_checked systemctl restart deployer-agent.service; else runtime_checked systemctl stop deployer-agent.service; fi
      fi
      ;;
  esac
  return "$runtime_status"
}

rollback_transaction() {
  rollback_status=0
  # Stop newly introduced units while their definitions still exist.
  if command -v systemctl >/dev/null 2>&1; then
    case "$ROLE" in server|all)
      if [ "$RESTART" = "1" ] && [ "$server_service_exists" = "0" ]; then
        systemctl stop deployer-server.service || rollback_status=1
      fi
      if [ "$ENABLE_TIMER" = "1" ] && [ "$server_timer_exists" = "0" ]; then
        systemctl disable --now deployer-auto-update-server.timer || rollback_status=1
      fi
      ;;
    esac
    case "$ROLE" in agent|all)
      if [ "$RESTART" = "1" ] && [ "$agent_service_exists" = "0" ]; then
        systemctl stop deployer-agent.service || rollback_status=1
      fi
      if [ "$ENABLE_TIMER" = "1" ] && [ "$agent_timer_exists" = "0" ]; then
        systemctl disable --now deployer-auto-update-agent.timer || rollback_status=1
      fi
      ;;
    esac
  fi
  restore_files || rollback_status=1
  if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || rollback_status=1
    restore_runtime_state || rollback_status=1
  fi
  if [ "$rollback_status" != "0" ]; then
    preserve_workdir="1"
    echo "platform file rollback failed; inspect the host before retrying" >&2
  fi
  return "$rollback_status"
}

snapshot_files
mutations_started="1"
transaction() (
  set -e
  case "$ROLE" in
    cli) install_cli ;;
    server) install_server ;;
    agent) install_agent ;;
    all) install_server; install_agent ;;
  esac
  if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
    if [ "$ENABLE_TIMER" = "1" ] && [ "$policy_mode" = "manual" ]; then
      case "$ROLE" in
        server) systemctl disable --now deployer-auto-update-server.timer ;;
        agent) systemctl disable --now deployer-auto-update-agent.timer ;;
        all) systemctl disable --now deployer-auto-update-server.timer deployer-auto-update-agent.timer ;;
      esac
    elif [ "$ENABLE_TIMER" = "1" ]; then
      case "$ROLE" in
        server) if [ "$timer_state" = "enabled" ] || [ "$timer_state" = "absent" ]; then systemctl enable --now deployer-auto-update-server.timer; fi ;;
        agent) if [ "$timer_state" = "enabled" ] || [ "$timer_state" = "absent" ]; then systemctl enable --now deployer-auto-update-agent.timer; fi ;;
        all)
          if [ "$server_timer_state" = "enabled" ] || { [ "$server_timer_state" = "absent" ] && [ "$policy_mode" != "manual" ]; }; then systemctl enable --now deployer-auto-update-server.timer; fi
          if [ "$agent_timer_state" = "enabled" ] || { [ "$agent_timer_state" = "absent" ] && [ "$policy_mode" != "manual" ]; }; then systemctl enable --now deployer-auto-update-agent.timer; fi
          ;;
      esac
    fi
    if [ "$RESTART" = "1" ]; then
      case "$ROLE" in
        server) systemctl restart deployer-server.service ;;
        agent) systemctl restart deployer-agent.service ;;
        all) systemctl restart deployer-server.service; systemctl restart deployer-agent.service ;;
      esac
    fi
  fi
  write_policy
)
set +e
transaction
transaction_status=$?
set -e
if [ "$transaction_status" -ne 0 ]; then
  original_status=$transaction_status
  echo "installation failed; restoring platform files" >&2
  rollback_transaction || true
  exit "$original_status"
fi

echo "Installed $REPO $VERSION ($PLATFORM) for role $ROLE"
echo "Update policy: mode=$policy_mode effective-version=${policy_version:-latest} timer-state=$timer_state"
