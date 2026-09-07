#!/bin/sh
set -eu

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/deployer-release-test.XXXXXX")
trap 'rm -rf "$ROOT"' EXIT INT TERM
FAKE="$ROOT/bin"
mkdir -p "$FAKE"

cat >"$FAKE/id" <<'EOF'
#!/bin/sh
[ "${1:-}" = "-u" ] && echo 0
EOF
cat >"$FAKE/flock" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$FAKE/systemctl" <<'EOF'
#!/bin/sh
case "${1:-}" in
  is-active|restart|daemon-reload) exit 0 ;;
  *) exit 0 ;;
esac
EOF
cat >"$FAKE/deployer-server" <<'EOF'
#!/bin/sh
echo 'version=v1.0.0 build=test'
EOF
cat >"$FAKE/deployer-install-release" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >"${DEPLOYER_INSTALL_LOG:?}"
exit 0
EOF
cat >"$FAKE/curl" <<'EOF'
#!/bin/sh
if printf '%s\n' "$*" | grep -q -- '-w'; then
  echo v1.2.0
fi
exit 0
EOF
chmod 0755 "$FAKE"/*

run_auto() {
  policy="$1"
  shift
  env PATH="$FAKE:$PATH" \
    DEPLOYER_UPDATE_POLICY_FILE="$policy" \
    DEPLOYER_UPDATE_LOCK="$ROOT/lock" \
    DEPLOYER_BIN_DIR="$FAKE" \
    DEPLOYER_SBIN_DIR="$FAKE" \
    DEPLOYER_SYSTEMD_DIR="$ROOT/systemd" \
    DEPLOYER_ROLLBACK_ROOT="$ROOT/rollback" \
    DEPLOYER_INSTALL_LOG="$ROOT/install.log" \
    sh scripts/auto-update.sh --role server "$@"
}

manual="$ROOT/manual.conf"
printf 'mode=manual\n' >"$manual"
if ! run_auto "$manual" >"$ROOT/manual.out"; then
  echo "manual policy unexpectedly failed" >&2
  exit 1
fi
grep -q 'automatic updates are disabled' "$ROOT/manual.out"

pinned="$ROOT/pinned.conf"
printf 'mode=pinned\nversion=v1.2.0\n' >"$pinned"
run_auto "$pinned" >"$ROOT/pinned.out"
grep -q -- '--version v1.2.0' "$ROOT/install.log"
grep -q -- '--policy-file ' "$ROOT/install.log"

downgrade="$ROOT/downgrade.conf"
printf 'mode=pinned\nversion=v0.9.0\n' >"$downgrade"
if run_auto "$downgrade" >"$ROOT/downgrade.out" 2>&1; then
  echo "pinned downgrade unexpectedly succeeded" >&2
  exit 1
fi

invalid="$ROOT/invalid.conf"
mkdir "$invalid"
if run_auto "$invalid" >"$ROOT/invalid.out" 2>&1; then
  echo "directory policy unexpectedly succeeded" >&2
  exit 1
fi

cat >"$FAKE/deployer-install-release" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod 0755 "$FAKE/deployer-install-release"
printf 'mode=pinned\nversion=v1.2.0\n' >"$ROOT/rollback.conf"
if run_auto "$ROOT/rollback.conf" >"$ROOT/rollback.out" 2>&1; then
  echo "failed installer unexpectedly succeeded" >&2
  exit 1
fi
grep -q '^v1.2.0$' "$ROOT/rollback/server/failed-version"
grep -q '^mode=pinned$' "$ROOT/rollback.conf"
grep -q '^version=v1.2.0$' "$ROOT/rollback.conf"
if run_auto "$ROOT/rollback.conf" >"$ROOT/quarantine.out" 2>&1; then
  grep -q 'skipping quarantined server release v1.2.0' "$ROOT/quarantine.out"
else
  echo "quarantined release retry unexpectedly failed" >&2
  exit 1
fi

echo "release control tests passed"
