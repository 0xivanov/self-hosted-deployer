#!/bin/sh
set -eu

REPO="0xivanov/self-hosted-deployer"
ROLE=""
POLICY_FILE=${DEPLOYER_UPDATE_POLICY_FILE:-/etc/deployer/update-policy.conf}
UPDATE_LOCK=${DEPLOYER_UPDATE_LOCK:-/run/deployer-auto-update.lock}
BIN_DIR=${DEPLOYER_BIN_DIR:-/usr/local/bin}
SBIN_DIR=${DEPLOYER_SBIN_DIR:-/usr/local/sbin}
SYSTEMD_DIR=${DEPLOYER_SYSTEMD_DIR:-/etc/systemd/system}
ROLLBACK_ROOT=${DEPLOYER_ROLLBACK_ROOT:-/var/lib/deployer/rollback}

usage() {
  echo "Usage: auto-update.sh --role server|agent [--repo OWNER/REPO] [--policy-file FILE]" >&2
}

policy_mode="latest"
policy_version=""

read_policy() {
  if [ -e "$POLICY_FILE" ] || [ -L "$POLICY_FILE" ]; then
    [ -f "$POLICY_FILE" ] || { echo "invalid update policy: path is not a regular file" >&2; return 1; }
  else
    return 0
  fi
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

while [ "$#" -gt 0 ]; do
  case "$1" in
    --role)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      ROLE="$2"
      shift 2
      ;;
    --repo)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      REPO="$2"
      shift 2
      ;;
    --policy-file)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      POLICY_FILE=$2
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

case "$ROLE" in
  server)
    BINARY=/usr/local/bin/deployer-server
    SERVICE=deployer-server.service
    ;;
  agent)
    BINARY=/usr/local/bin/deployer-agent
    SERVICE=deployer-agent.service
    ;;
  *)
    usage
    exit 2
    ;;
esac

[ "$(id -u)" = "0" ] || { echo "auto-update must run as root" >&2; exit 1; }
command -v flock >/dev/null 2>&1 || { echo "flock is required" >&2; exit 1; }
read_policy || exit 1
if [ "$policy_mode" != "manual" ]; then
  command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
fi

exec 9>"$UPDATE_LOCK"
if ! flock -n 9; then
  echo "another deployer update is already running"
  exit 0
fi

case "$ROLE" in
  server) BINARY="$BIN_DIR/deployer-server" ;;
  agent) BINARY="$BIN_DIR/deployer-agent" ;;
esac
current=$($BINARY version | sed -n 's/^version=\([^ ]*\).*/\1/p')
[ -n "$current" ] || { echo "cannot determine installed version" >&2; exit 1; }

echo "$ROLE update policy=${policy_mode} effective-version=${policy_version:-latest} installed-version=$current"
if [ "$policy_mode" = "manual" ]; then
  echo "automatic updates are disabled by the manual update policy"
  exit 0
fi

if [ "$policy_mode" = "pinned" ]; then
  latest=$policy_version
else
  latest_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
  latest=${latest_url##*/}
fi
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
    case "$component" in
      ''|*[!0-9]*) return 1 ;;
    esac
  done
  printf '%s:%s:%s:%s\n' "$major" "$minor" "$patch" "${prerelease:-_release}"
}

valid_release_tag() {
  semver_parts "$1" >/dev/null 2>&1
}

valid_release_tag "$latest" || { echo "invalid release tag: $latest" >&2; exit 1; }
current_parts=$(semver_parts "$current") || { echo "invalid installed version: $current" >&2; exit 1; }
latest_parts=$(semver_parts "$latest") || { echo "invalid latest release version: $latest" >&2; exit 1; }
IFS=: read -r current_major current_minor current_patch current_prerelease <<EOF
$current_parts
EOF
IFS=: read -r latest_major latest_minor latest_patch latest_prerelease <<EOF
$latest_parts
EOF

if [ "$current" = "$latest" ]; then
  echo "$ROLE is already current at $current"
  exit 0
fi

forward="0"
if [ "$latest_major" -gt "$current_major" ]; then
  forward="1"
elif [ "$latest_major" -eq "$current_major" ] && [ "$latest_minor" -gt "$current_minor" ]; then
  forward="1"
elif [ "$latest_major" -eq "$current_major" ] && [ "$latest_minor" -eq "$current_minor" ] && [ "$latest_patch" -gt "$current_patch" ]; then
  forward="1"
elif [ "$latest_major" -eq "$current_major" ] && [ "$latest_minor" -eq "$current_minor" ] && \
  [ "$latest_patch" -eq "$current_patch" ] && [ "$current_prerelease" != "_release" ] && \
  [ "$latest_prerelease" = "_release" ]; then
  forward="1"
fi
if [ "$forward" != "1" ]; then
  echo "refusing non-forward deployer update from $current to $latest" >&2
  exit 1
fi

rollback_dir="$ROLLBACK_ROOT/$ROLE"
failed_version_file="$rollback_dir/failed-version"
install -d -m 0700 "$rollback_dir"
if [ -f "$failed_version_file" ]; then
  failed_version=$(sed -n '1p' "$failed_version_file")
  if [ "$failed_version" = "$latest" ]; then
    echo "skipping quarantined $ROLE release $latest after its previous failed update"
    exit 0
  fi
  rm -f "$failed_version_file"
fi
snapshot_dir="$rollback_dir/$current-files"
rm -rf "$snapshot_dir"
install -d -m 0700 "$snapshot_dir"

snapshot_file() {
  source_path=$1
  snapshot_name=$2
  if [ -f "$source_path" ]; then
    cp -p "$source_path" "$snapshot_dir/$snapshot_name"
  else
    : >"$snapshot_dir/$snapshot_name.absent"
  fi
}

restore_file() {
  target_path=$1
  snapshot_name=$2
  target_mode=$3
  if [ -f "$snapshot_dir/$snapshot_name.absent" ]; then
    rm -f "$target_path"
    return
  fi
  temporary_path="$target_path.deployer-rollback.$$"
  install -m "$target_mode" "$snapshot_dir/$snapshot_name" "$temporary_path"
  mv -f "$temporary_path" "$target_path"
}

case "$ROLE" in
  server)
    role_unit=deployer-server.service
    updater_unit=deployer-auto-update-server.service
    timer_unit=deployer-auto-update-server.timer
    ;;
  agent)
    role_unit=deployer-agent.service
    updater_unit=deployer-auto-update-agent.service
    timer_unit=deployer-auto-update-agent.timer
    ;;
esac

snapshot_file "$BIN_DIR/deployer" cli
snapshot_file "$BINARY" role-binary
snapshot_file "$SBIN_DIR/deployer-auto-update" updater
snapshot_file "$SBIN_DIR/deployer-install-release" installer
snapshot_file "$SYSTEMD_DIR/$role_unit" role-unit
snapshot_file "$SYSTEMD_DIR/$updater_unit" updater-unit
snapshot_file "$SYSTEMD_DIR/$timer_unit" timer-unit
snapshot_file "$POLICY_FILE" update-policy

restore_release() {
  restore_file "$BIN_DIR/deployer" cli 0755
  restore_file "$BINARY" role-binary 0755
  restore_file "$SBIN_DIR/deployer-auto-update" updater 0755
  restore_file "$SBIN_DIR/deployer-install-release" installer 0755
  restore_file "$SYSTEMD_DIR/$role_unit" role-unit 0644
  restore_file "$SYSTEMD_DIR/$updater_unit" updater-unit 0644
  restore_file "$SYSTEMD_DIR/$timer_unit" timer-unit 0644
  restore_file "$POLICY_FILE" update-policy 0644
}

installer="$SBIN_DIR/deployer-install-release"
if [ ! -x "$installer" ]; then
  echo "trusted local release installer is missing: $installer" >&2
  exit 1
fi
if ! "$installer" --repo "$REPO" --version "$latest" --role "$ROLE" --policy-file "$POLICY_FILE" --no-restart --no-enable-timer; then
  echo "release installation failed; restoring $current files" >&2
  printf '%s\n' "$latest" >"$failed_version_file"
  restore_release
  systemctl daemon-reload
  exit 1
fi

if systemctl restart "$SERVICE" && systemctl is-active --quiet "$SERVICE"; then
  if [ "$ROLE" != "server" ] || curl -fsS --max-time 10 http://127.0.0.1:7080/readyz >/dev/null; then
    rm -f "$failed_version_file"
    echo "updated $ROLE from $current to $latest"
    exit 0
  fi
fi

echo "health check failed; restoring $current" >&2
printf '%s\n' "$latest" >"$failed_version_file"
restore_release
systemctl daemon-reload
systemctl restart "$SERVICE"
systemctl is-active --quiet "$SERVICE"
exit 1
