#!/bin/sh
set -eu

usage() {
  cat >&2 <<'USAGE'
Usage: backup-platform-offsite.sh \
  --server-binary PATH \
  --database-path PATH \
  --backup-key-file PATH \
  --work-root PATH \
  --environment TAG \
  --restic-binary PATH

Creates an encrypted platform SQLite artifact, uploads it to an existing
RESTIC_REPOSITORY using RESTIC_PASSWORD_FILE, and verifies the same snapshot
by reading back the encrypted artifact into an isolated temporary directory.
USAGE
}

SERVER_BINARY=""
DATABASE_PATH=""
BACKUP_KEY_FILE=""
WORK_ROOT=""
ENVIRONMENT_TAG=""
RESTIC_BINARY=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --server-binary) [ "$#" -ge 2 ] || { usage; exit 2; }; SERVER_BINARY=$2; shift 2 ;;
    --database-path) [ "$#" -ge 2 ] || { usage; exit 2; }; DATABASE_PATH=$2; shift 2 ;;
    --backup-key-file) [ "$#" -ge 2 ] || { usage; exit 2; }; BACKUP_KEY_FILE=$2; shift 2 ;;
    --work-root) [ "$#" -ge 2 ] || { usage; exit 2; }; WORK_ROOT=$2; shift 2 ;;
    --environment) [ "$#" -ge 2 ] || { usage; exit 2; }; ENVIRONMENT_TAG=$2; shift 2 ;;
    --restic-binary) [ "$#" -ge 2 ] || { usage; exit 2; }; RESTIC_BINARY=$2; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

fail() {
  echo "platform backup job failed: $1" >&2
  exit 1
}

[ -n "$SERVER_BINARY" ] || { usage; exit 2; }
[ -n "$DATABASE_PATH" ] || { usage; exit 2; }
[ -n "$BACKUP_KEY_FILE" ] || { usage; exit 2; }
[ -n "$WORK_ROOT" ] || { usage; exit 2; }
[ -n "$ENVIRONMENT_TAG" ] || { usage; exit 2; }
[ -n "$RESTIC_BINARY" ] || { usage; exit 2; }

command -v python3 >/dev/null 2>&1 || fail "python3 is required"
if ! python3 - "$SERVER_BINARY" "$RESTIC_BINARY" "$DATABASE_PATH" "$BACKUP_KEY_FILE" "$WORK_ROOT" "$ENVIRONMENT_TAG" <<'VALIDATE'
import os, pathlib, re, stat, sys
server, restic, database, key, work, environment = sys.argv[1:]
try:
    assert re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,62}", environment)
    for path in (server, restic, database, key, work):
        assert pathlib.Path(path).is_absolute()
    for binary in (server, restic):
        assert pathlib.Path(binary).is_file() and os.access(binary, os.X_OK)
    root = pathlib.Path(work)
    mode = root.lstat()
    assert stat.S_ISDIR(mode.st_mode) and stat.S_IMODE(mode.st_mode) == 0o700 and mode.st_uid == os.geteuid()
    for path in (database, key):
        item = pathlib.Path(path)
        assert stat.S_ISREG(item.lstat().st_mode)
        assert not item.resolve().is_relative_to(root.resolve())
except (OSError, AssertionError):
    sys.exit(1)
VALIDATE
then
  fail "invalid paths, private work directory, or environment tag"
fi

if [ -z "${RESTIC_REPOSITORY+x}" ] || [ -z "$RESTIC_REPOSITORY" ]; then
  fail "RESTIC_REPOSITORY is required"
fi
if [ -z "${RESTIC_PASSWORD_FILE+x}" ] || [ -z "$RESTIC_PASSWORD_FILE" ]; then
  fail "RESTIC_PASSWORD_FILE is required"
fi
[ -z "${RESTIC_PASSWORD+x}" ] || fail "RESTIC_PASSWORD is not allowed; use RESTIC_PASSWORD_FILE"
[ -z "${RESTIC_PASSWORD_COMMAND+x}" ] || fail "RESTIC_PASSWORD_COMMAND is not allowed; use RESTIC_PASSWORD_FILE"
[ -z "${RESTIC_REPOSITORY_FILE+x}" ] || fail "RESTIC_REPOSITORY_FILE is not allowed; use RESTIC_REPOSITORY"
if ! python3 - "$RESTIC_PASSWORD_FILE" <<'PASSWORD'
import os, pathlib, stat, sys
try:
    path = pathlib.Path(sys.argv[1])
    mode = path.lstat()
    assert path.is_absolute() and stat.S_ISREG(mode.st_mode) and mode.st_size > 0
    assert mode.st_mode & 0o077 == 0 and mode.st_uid == os.geteuid()
except (OSError, AssertionError):
    sys.exit(1)
PASSWORD
then
  fail "restic password file must be private, owned, nonempty, and regular"
fi

umask 077
RUN_DIR=$(mktemp -d "$WORK_ROOT/platform-backup.XXXXXX") || fail "create private run directory"
cleanup() { rm -rf "$RUN_DIR"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

ARTIFACT="$RUN_DIR/platform.backup"
BACKUP_JSON="$RUN_DIR/restic-backup.jsonl"
SNAPSHOTS_JSON="$RUN_DIR/restic-snapshots.json"
RESTORED_ARTIFACT="$RUN_DIR/restored.backup"

if ! "$SERVER_BINARY" backup create \
  --database-path "$DATABASE_PATH" \
  --output "$ARTIFACT" \
  --key-file "$BACKUP_KEY_FILE" \
  >"$RUN_DIR/server.stdout" 2>"$RUN_DIR/server.stderr"; then
  fail "create encrypted SQLite artifact"
fi
[ -f "$ARTIFACT" ] || fail "server did not create encrypted SQLite artifact"

if ! "$RESTIC_BINARY" --no-cache backup --json --tag "$ENVIRONMENT_TAG" --stdin --stdin-filename platform.backup <"$ARTIFACT" \
  >"$BACKUP_JSON" 2>"$RUN_DIR/restic-backup.stderr"; then
  fail "upload encrypted SQLite artifact"
fi

SNAPSHOT_ID=$(python3 - "$BACKUP_JSON" <<'PY'
import json
import re
import sys

found = []
try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        for line in handle:
            if not line.strip():
                continue
            item = json.loads(line)
            if not isinstance(item, dict):
                sys.exit(1)
            if item.get("message_type") == "summary":
                snapshot_id = item.get("snapshot_id")
                if isinstance(snapshot_id, str) and re.fullmatch(r"[0-9a-f]{64}", snapshot_id):
                    found.append(snapshot_id)
except (OSError, ValueError, TypeError):
    sys.exit(1)
if len(found) != 1:
    sys.exit(1)
print(found[0])
PY
) || fail "parse upload snapshot id"

if ! "$RESTIC_BINARY" --no-cache snapshots --json "$SNAPSHOT_ID" \
  >"$SNAPSHOTS_JSON" 2>"$RUN_DIR/restic-snapshots.stderr"; then
  fail "read uploaded snapshot"
fi

if ! python3 - "$SNAPSHOTS_JSON" "$SNAPSHOT_ID" "$ENVIRONMENT_TAG" <<'PY'
import json
import re
import sys

path, expected_id, expected_tag = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as handle:
        snapshots = json.load(handle)
except (OSError, ValueError, TypeError):
    sys.exit(1)
if not isinstance(snapshots, list):
    sys.exit(1)
matches = [item for item in snapshots if isinstance(item, dict) and item.get("id") == expected_id]
if len(matches) != 1 or not re.fullmatch(r"[0-9a-f]{64}", expected_id):
    sys.exit(1)
tags = matches[0].get("tags")
if not isinstance(tags, list) or expected_tag not in tags:
    sys.exit(1)
PY
then
  fail "validate exact uploaded snapshot"
fi

# Read back only the fixed artifact from the exact snapshot, with no cache.
if ! "$RESTIC_BINARY" --no-cache dump "$SNAPSHOT_ID" /platform.backup \
  >"$RESTORED_ARTIFACT" 2>"$RUN_DIR/restic-dump.stderr"; then
  fail "read back exact uploaded snapshot"
fi
if ! cmp -s "$ARTIFACT" "$RESTORED_ARTIFACT"; then
  fail "compare downloaded artifact"
fi

echo "repository round-trip verified: environment=$ENVIRONMENT_TAG snapshot=$SNAPSHOT_ID"
