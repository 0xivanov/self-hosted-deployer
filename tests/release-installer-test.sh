#!/bin/sh
set -eu
[ -f /.dockerenv ] || { echo "run this test only inside disposable Docker" >&2; exit 2; }
ROOT=$(mktemp -d /tmp/deployer-installer-test.XXXXXX)
trap 'rm -rf "$ROOT"; rm -f /usr/local/bin/deployer /usr/local/bin/deployer-server /usr/local/bin/deployer-agent /usr/local/sbin/deployer-auto-update /usr/local/sbin/deployer-install-release; rm -f /etc/systemd/system/deployer-*' EXIT INT TERM
FAKE="$ROOT/fake"; RELEASE="$ROOT/release"
mkdir -p "$FAKE" "$RELEASE/deployer-linux-amd64/scripts" "$RELEASE/deployer-linux-amd64/deploy/systemd"
printf '#!/bin/sh\necho cli\n' >"$RELEASE/deployer-linux-amd64/deployer"
printf '#!/bin/sh\necho "version=v1.2.3 build=test"\n' >"$RELEASE/deployer-linux-amd64/deployer-server"
printf '#!/bin/sh\necho "version=v1.2.3 build=test"\n' >"$RELEASE/deployer-linux-amd64/deployer-agent"
cp scripts/auto-update.sh "$RELEASE/deployer-linux-amd64/scripts/auto-update.sh"
cp scripts/install-release.sh "$RELEASE/deployer-linux-amd64/scripts/install-release.sh"
for unit in deployer-server.service deployer-agent.service deployer-auto-update-server.service deployer-auto-update-agent.service deployer-auto-update-server.timer deployer-auto-update-agent.timer; do printf '[Unit]\nDescription=test\n' >"$RELEASE/deployer-linux-amd64/deploy/systemd/$unit"; done
chmod 0755 "$RELEASE/deployer-linux-amd64"/deployer* "$RELEASE/deployer-linux-amd64/scripts"/*
(cd "$RELEASE" && tar -czf deployer-linux-amd64.tar.gz deployer-linux-amd64)
sha256sum "$RELEASE/deployer-linux-amd64.tar.gz" | sed 's#  .*#  deployer-linux-amd64.tar.gz#' >"$RELEASE/checksums.txt"

# shellcheck disable=SC2016
printf '#!/bin/sh\nset -eu\nurl=$2; out=$4\ncase "$url" in */checksums.txt) cp "$DEPLOYER_TEST_RELEASE/checksums.txt" "$out" ;; *) cp "$DEPLOYER_TEST_RELEASE/deployer-linux-amd64.tar.gz" "$out" ;; esac\n' >"$FAKE/curl"
# shellcheck disable=SC2016
printf '#!/bin/sh\nset -eu\nlog=${DEPLOYER_TEST_SYSTEMCTL_LOG:?}; state=${DEPLOYER_TEST_SYSTEMCTL_STATE:?}; cmd=${1:-}; unit=${2:-}; printf "%%s\\n" "$*" >>"$log"\ncase "$cmd" in is-enabled) value=$(awk -F= -v unit="$unit" '\''$1 == unit { print $2}'\'' "$state"); [ -n "$value" ] || value=absent; case "$value" in enabled|disabled|masked|static|indirect) echo "$value"; exit 0 ;; *) exit 1 ;; esac ;; restart) if [ "${DEPLOYER_TEST_FAIL_RESTART:-0}" = 1 ]; then count=0; [ -f "$DEPLOYER_TEST_RESTART_COUNT" ] && count=$(cat "$DEPLOYER_TEST_RESTART_COUNT"); if [ "$count" = 0 ]; then echo 1 >"$DEPLOYER_TEST_RESTART_COUNT"; exit 1; fi; fi; exit 0 ;; enable|disable|daemon-reload|is-active|status|list-unit-files) exit 0 ;; *) exit 0 ;; esac\n' >"$FAKE/systemctl"
# shellcheck disable=SC2016
printf '#!/bin/sh\ncase "${DEPLOYER_TEST_FAIL_ROLLBACK_CP:-0}:$*" in 1:*deployer-rollback*) exit 1 ;; esac\nexec /bin/cp "$@"\n' >"$FAKE/cp"
chmod 0755 "$FAKE"/*

run_installer() { policy=$1; state=$2; shift 2; : >"$ROOT/systemctl.log"; printf '%s\n' "$state" >"$ROOT/systemctl-state"; env PATH="$FAKE:$PATH" DEPLOYER_TEST_RELEASE="$RELEASE" DEPLOYER_TEST_SYSTEMCTL_LOG="$ROOT/systemctl.log" DEPLOYER_TEST_SYSTEMCTL_STATE="$ROOT/systemctl-state" DEPLOYER_UPDATE_POLICY_FILE="$policy" DEPLOYER_UPDATE_LOCK="$ROOT/update.lock" sh scripts/install-release.sh --repo test/repo --role server --no-restart "$@"; }

policy="$ROOT/policy.conf"; printf 'mode=pinned\nversion=v1.2.3\n' >"$policy"; run_installer "$policy" 'deployer-auto-update-server.timer=disabled'; if grep -q 'enable --now' "$ROOT/systemctl.log"; then exit 1; fi; grep -q '^mode=pinned$' "$policy"; grep -q '^version=v1.2.3$' "$policy"
agent_policy="$ROOT/agent-policy.conf"; printf 'mode=latest\n' >"$agent_policy"; : >"$ROOT/systemctl.log"; printf 'deployer-auto-update-agent.timer=masked\n' >"$ROOT/systemctl-state"; rm -f /etc/systemd/system/deployer-auto-update-agent.timer; ln -s /dev/null /etc/systemd/system/deployer-auto-update-agent.timer; env PATH="$FAKE:$PATH" DEPLOYER_TEST_RELEASE="$RELEASE" DEPLOYER_TEST_SYSTEMCTL_LOG="$ROOT/systemctl.log" DEPLOYER_TEST_SYSTEMCTL_STATE="$ROOT/systemctl-state" DEPLOYER_UPDATE_POLICY_FILE="$agent_policy" DEPLOYER_UPDATE_LOCK="$ROOT/agent.lock" sh scripts/install-release.sh --repo test/repo --role agent --no-restart; if grep -q 'enable --now' "$ROOT/systemctl.log"; then exit 1; fi; [ -L /etc/systemd/system/deployer-auto-update-agent.timer ]; [ "$(readlink /etc/systemd/system/deployer-auto-update-agent.timer)" = /dev/null ]
all_policy="$ROOT/all-policy.conf"; printf 'mode=latest\n' >"$all_policy"; : >"$ROOT/systemctl.log"; printf 'deployer-auto-update-server.timer=disabled\ndeployer-auto-update-agent.timer=enabled\n' >"$ROOT/systemctl-state"; env PATH="$FAKE:$PATH" DEPLOYER_TEST_RELEASE="$RELEASE" DEPLOYER_TEST_SYSTEMCTL_LOG="$ROOT/systemctl.log" DEPLOYER_TEST_SYSTEMCTL_STATE="$ROOT/systemctl-state" DEPLOYER_UPDATE_POLICY_FILE="$all_policy" DEPLOYER_UPDATE_LOCK="$ROOT/all.lock" sh scripts/install-release.sh --repo test/repo --role all --no-restart; if grep -q 'enable --now deployer-auto-update-server.timer' "$ROOT/systemctl.log"; then exit 1; fi; grep -q 'enable --now deployer-auto-update-agent.timer' "$ROOT/systemctl.log"
manual_policy="$ROOT/manual-policy.conf"; printf 'mode=manual\n' >"$manual_policy"; : >"$ROOT/systemctl.log"; printf 'deployer-auto-update-server.timer=enabled\n' >"$ROOT/systemctl-state"; env PATH="$FAKE:$PATH" DEPLOYER_TEST_RELEASE="$RELEASE" DEPLOYER_TEST_SYSTEMCTL_LOG="$ROOT/systemctl.log" DEPLOYER_TEST_SYSTEMCTL_STATE="$ROOT/systemctl-state" DEPLOYER_UPDATE_POLICY_FILE="$manual_policy" DEPLOYER_UPDATE_LOCK="$ROOT/manual.lock" sh scripts/install-release.sh --repo test/repo --role server --version v1.2.3 --no-restart; grep -q 'disable --now deployer-auto-update-server.timer' "$ROOT/systemctl.log"
mismatch="$ROOT/mismatch.conf"; printf 'mode=pinned\nversion=v1.2.3\n' >"$mismatch"; if run_installer "$mismatch" 'deployer-auto-update-server.timer=disabled' --version v1.2.4; then exit 1; fi; if grep -q daemon-reload "$ROOT/systemctl.log"; then exit 1; fi
failed_policy="$ROOT/failed-policy.conf"; printf '#!/bin/sh\necho old-server\n' >/usr/local/bin/deployer-server; chmod 0755 /usr/local/bin/deployer-server; printf 'deployer-auto-update-server.timer=disabled\n' >"$ROOT/systemctl-state"; rm -f "$ROOT/restart-count"; if env PATH="$FAKE:$PATH" DEPLOYER_TEST_RELEASE="$RELEASE" DEPLOYER_TEST_SYSTEMCTL_LOG="$ROOT/systemctl.log" DEPLOYER_TEST_SYSTEMCTL_STATE="$ROOT/systemctl-state" DEPLOYER_TEST_RESTART_COUNT="$ROOT/restart-count" DEPLOYER_TEST_FAIL_RESTART=1 DEPLOYER_UPDATE_POLICY_FILE="$failed_policy" DEPLOYER_UPDATE_LOCK="$ROOT/failed.lock" sh scripts/install-release.sh --repo test/repo --role server --version v1.2.3 --update-policy pinned; then exit 1; fi; [ ! -e "$failed_policy" ]; grep -q old-server /usr/local/bin/deployer-server; [ "$(grep -c '^restart ' "$ROOT/systemctl.log")" -ge 2 ]
cp_failure_policy="$ROOT/cp-failure-policy.conf"; rm -f "$cp_failure_policy" "$ROOT/restart-count"; if env PATH="$FAKE:$PATH" DEPLOYER_TEST_RELEASE="$RELEASE" DEPLOYER_TEST_SYSTEMCTL_LOG="$ROOT/systemctl.log" DEPLOYER_TEST_SYSTEMCTL_STATE="$ROOT/systemctl-state" DEPLOYER_TEST_RESTART_COUNT="$ROOT/restart-count" DEPLOYER_TEST_FAIL_RESTART=1 DEPLOYER_TEST_FAIL_ROLLBACK_CP=1 DEPLOYER_UPDATE_POLICY_FILE="$cp_failure_policy" DEPLOYER_UPDATE_LOCK="$ROOT/cp-failure.lock" sh scripts/install-release.sh --repo test/repo --role server --version v1.2.3 --update-policy pinned >"$ROOT/cp-failure.out" 2>&1; then exit 1; fi; grep -q 'installer rollback snapshot preserved at' "$ROOT/cp-failure.out"
# Failed first install must stop newly started units before deleting their files.
rm -f /usr/local/bin/deployer-server /etc/systemd/system/deployer-server.service /etc/systemd/system/deployer-auto-update-server.timer "$ROOT/restart-count"
: >"$ROOT/systemctl-state"
: >"$ROOT/systemctl.log"
if env PATH="$FAKE:$PATH" DEPLOYER_TEST_RELEASE="$RELEASE" DEPLOYER_TEST_SYSTEMCTL_LOG="$ROOT/systemctl.log" DEPLOYER_TEST_SYSTEMCTL_STATE="$ROOT/systemctl-state" DEPLOYER_TEST_RESTART_COUNT="$ROOT/restart-count" DEPLOYER_TEST_FAIL_RESTART=1 DEPLOYER_UPDATE_POLICY_FILE="$ROOT/fresh-policy" DEPLOYER_UPDATE_LOCK="$ROOT/fresh.lock" sh scripts/install-release.sh --repo test/repo --role server --version v1.2.3; then exit 1; fi
[ ! -e /usr/local/bin/deployer-server ]
[ ! -e /etc/systemd/system/deployer-server.service ]
grep -q '^stop deployer-server.service$' "$ROOT/systemctl.log"
grep -q '^disable --now deployer-auto-update-server.timer$' "$ROOT/systemctl.log"
echo "release installer tests passed"
