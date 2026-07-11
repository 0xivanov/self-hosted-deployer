#!/bin/sh
set -eu

REPO="0xivanov/self-hosted-deployer"
ROLE=""

usage() {
  echo "Usage: auto-update.sh --role server|agent [--repo OWNER/REPO]" >&2
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
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
command -v flock >/dev/null 2>&1 || { echo "flock is required" >&2; exit 1; }

exec 9>/run/deployer-auto-update.lock
if ! flock -n 9; then
  echo "another deployer update is already running"
  exit 0
fi

current=$($BINARY version | sed -n 's/^version=\([^ ]*\).*/\1/p')
[ -n "$current" ] || { echo "cannot determine installed version" >&2; exit 1; }

latest_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
latest=${latest_url##*/}
case "$latest" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "invalid latest release tag: $latest" >&2; exit 1 ;;
esac

if [ "$current" = "$latest" ]; then
  echo "$ROLE is already current at $current"
  exit 0
fi

workdir=$(mktemp -d "${TMPDIR:-/tmp}/deployer-auto-update.XXXXXX")
trap 'rm -rf "$workdir"' EXIT INT TERM

rollback_dir="/var/lib/deployer/rollback/$ROLE"
install -d -m 0700 "$rollback_dir"
cp "$BINARY" "$rollback_dir/$current"
chmod 0755 "$rollback_dir/$current"

installer="$workdir/install-release.sh"
curl -fsSL "https://raw.githubusercontent.com/$REPO/$latest/scripts/install-release.sh" -o "$installer"
sh "$installer" --repo "$REPO" --version "$latest" --role "$ROLE" --no-restart

if systemctl restart "$SERVICE" && systemctl is-active --quiet "$SERVICE"; then
  if [ "$ROLE" != "server" ] || curl -fsS --max-time 10 http://127.0.0.1:7080/readyz >/dev/null; then
    echo "updated $ROLE from $current to $latest"
    exit 0
  fi
fi

echo "health check failed; restoring $current" >&2
install -m 0755 "$rollback_dir/$current" "$BINARY"
systemctl restart "$SERVICE"
systemctl is-active --quiet "$SERVICE"
exit 1
