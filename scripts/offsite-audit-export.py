#!/usr/bin/env python3
"""Forward the safe JSON audit journal to a restic repository.

The journal is treated as an append-only JSONL source.  A checkpoint is
advanced only after the exact uploaded artifact has been read back and
verified, so an interrupted run retries records rather than losing them.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import datetime as dt

ENV_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$")
SNAP_RE = re.compile(r"^[0-9a-f]{64}$")
ALLOWED = {"timestamp", "correlation_id", "token_id", "caller_kind", "node_id", "method", "target", "outcome"}
ENVELOPE = ALLOWED | {"msg", "component", "level"}
IDENTIFIER_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$")
METHOD_TARGETS = {
    "/deployer.v1.AppService/DeployApp": {"app"}, "/deployer.v1.AppService/DeleteApp": {"app"},
    "/deployer.v1.NodeService/CreateJoinToken": {"node"}, "/deployer.v1.NodeService/DrainNode": {"node"},
    "/deployer.v1.NodeService/UncordonNode": {"node"}, "/deployer.v1.NodeService/RemoveNode": {"node"},
    "/deployer.v1.NodeService/PurgeNode": {"node"}, "/deployer.v1.NodeService/RenameNode": {"node", "new_name"},
    "/deployer.v1.SecretService/SetSecret": {"app", "secret"}, "/deployer.v1.SecretService/DeleteSecret": {"app", "secret"},
}
CALLERS = {"admin", "agent", "join"}
OUTCOMES = {"OK", "Canceled", "Unknown", "InvalidArgument", "DeadlineExceeded", "NotFound", "AlreadyExists", "PermissionDenied", "ResourceExhausted", "FailedPrecondition", "Aborted", "OutOfRange", "Unimplemented", "Internal", "Unavailable", "DataLoss", "Unauthenticated"}


class ExportError(Exception):
    pass


def private_file(path: Path, label: str, *, writable=False) -> None:
    try:
        st = path.lstat()
    except OSError as exc:
        raise ExportError(f"invalid {label}") from exc
    if path.is_symlink() or not path.is_file() or st.st_uid != os.geteuid() or st.st_mode & 0o077:
        raise ExportError(f"invalid {label}")

def executable_file(path: Path) -> None:
    try:
        st = path.lstat()
    except OSError as exc:
        raise ExportError("invalid restic binary") from exc
    if path.is_symlink() or not path.is_file() or st.st_uid != os.geteuid() or st.st_mode & 0o022 or not os.access(path, os.X_OK):
        raise ExportError("invalid restic binary")


def restic_env() -> None:
    if not os.environ.get("RESTIC_REPOSITORY") or not os.environ.get("RESTIC_PASSWORD_FILE"):
        raise ExportError("RESTIC_REPOSITORY and RESTIC_PASSWORD_FILE are required")
    if any(x in os.environ for x in ("RESTIC_PASSWORD", "RESTIC_PASSWORD_COMMAND", "RESTIC_REPOSITORY_FILE")):
        raise ExportError("restic password alternatives are not allowed")
    private_file(Path(os.environ["RESTIC_PASSWORD_FILE"]), "restic password file")


def run(restic: Path, args: list[str], *, cwd: Path, stdin=None, stdout=None) -> subprocess.CompletedProcess:
    try:
        return subprocess.run([str(restic), "--no-cache", *args], cwd=cwd, stdin=stdin, stdout=stdout,
                              stderr=subprocess.PIPE, check=False)
    except OSError as exc:
        raise ExportError("restic operation failed") from exc


def safe_record(item: object, line_number: int) -> dict:
    if not isinstance(item, dict) or set(item) - ENVELOPE or item.get("msg") not in (None, "mutation audit"):
        raise ExportError(f"invalid audit record at line {line_number}")
    if item.get("level") not in (None, "INFO"):
        raise ExportError(f"invalid audit record at line {line_number}")
    result = {}
    for key in ALLOWED:
        value = item.get(key)
        if key == "target":
            if value is None:
                continue
            if not isinstance(value, dict) or any(not isinstance(k, str) or k not in METHOD_TARGETS.get(item.get("method"), set()) or not isinstance(v, str) or not IDENTIFIER_RE.fullmatch(v) for k, v in value.items()):
                raise ExportError(f"invalid audit record at line {line_number}")
            result[key] = {k: value[k] for k in sorted(value)}
        elif value is not None:
            if not isinstance(value, (str, int, float, bool)):
                raise ExportError(f"invalid audit record at line {line_number}")
            result[key] = value
    timestamp = result.get("timestamp")
    try:
        parsed_timestamp = dt.datetime.fromisoformat(timestamp.replace("Z", "+00:00")) if isinstance(timestamp, str) else None
    except ValueError:
        parsed_timestamp = None
    if parsed_timestamp is None or parsed_timestamp.tzinfo is None or result.get("method") not in METHOD_TARGETS or result.get("outcome") not in OUTCOMES:
        raise ExportError(f"invalid audit record at line {line_number}")
    for key in ("correlation_id", "token_id"):
        if not isinstance(result.get(key), str) or not IDENTIFIER_RE.fullmatch(result[key]):
            raise ExportError(f"invalid audit record at line {line_number}")
    if result.get("caller_kind") not in CALLERS:
        raise ExportError(f"invalid audit record at line {line_number}")
    if result.get("node_id") not in (None, "") and (not isinstance(result["node_id"], str) or not IDENTIFIER_RE.fullmatch(result["node_id"])):
        raise ExportError(f"invalid audit record at line {line_number}")
    return {key: result[key] for key in sorted(result)}


def source_fingerprint(path: Path, end: int) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        remaining = end
        while remaining:
            block = handle.read(min(1024 * 1024, remaining))
            if not block:
                raise ExportError("audit source changed during export")
            digest.update(block)
            remaining -= len(block)
    return digest.hexdigest()


def load_checkpoint(path: Path, source: Path) -> tuple[int, dict]:
    if not path.exists():
        return 0, {}
    private_file(path, "checkpoint")
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
        stat = source.stat()
    except (OSError, ValueError, TypeError) as exc:
        raise ExportError("invalid audit checkpoint") from exc
    if not isinstance(data, dict) or not isinstance(data.get("offset"), int) or data["offset"] < 0:
        raise ExportError("invalid audit checkpoint")
    if data.get("device") != stat.st_dev or data.get("inode") != stat.st_ino or data.get("fingerprint") != source_fingerprint(source, data["offset"]):
        raise ExportError("audit source rotated or checkpoint invalid")
    return data["offset"], data


def upload_verify(restic: Path, artifact: Path, environment: str, run_dir: Path, source_end: int) -> str:
    response = run_dir / "upload.jsonl"
    with artifact.open("rb") as source, response.open("wb") as output:
        if run(restic, ["backup", "--json", "--tag", environment, "--tag", "audit", "--stdin", "--stdin-filename", "audit.jsonl"], cwd=run_dir, stdin=source, stdout=output).returncode != 0:
            raise ExportError("audit upload failed")
    try:
        records = [json.loads(line) for line in response.read_text(encoding="utf-8").splitlines() if line.strip()]
        ids = [x["snapshot_id"] for x in records if isinstance(x, dict) and x.get("message_type") == "summary" and isinstance(x.get("snapshot_id"), str) and SNAP_RE.fullmatch(x["snapshot_id"])]
    except (OSError, ValueError, TypeError, KeyError) as exc:
        raise ExportError("audit upload response invalid") from exc
    if len(ids) != 1:
        raise ExportError("audit upload response invalid")
    sid = ids[0]
    snapshots = run_dir / "snapshots.json"
    with snapshots.open("wb") as output:
        if run(restic, ["snapshots", "--json", sid], cwd=run_dir, stdout=output).returncode != 0:
            raise ExportError("audit snapshot verification failed")
    try:
        listed = json.loads(snapshots.read_text(encoding="utf-8"))
        matches = [x for x in listed if isinstance(x, dict) and x.get("id") == sid]
        if len(matches) != 1 or environment not in matches[0].get("tags", []) or "audit" not in matches[0].get("tags", []):
            raise ExportError("audit snapshot verification failed")
    except (OSError, ValueError, TypeError) as exc:
        raise ExportError("audit snapshot verification failed") from exc
    restored = run_dir / "restored.jsonl"
    with restored.open("wb") as output:
        if run(restic, ["dump", sid, "/audit.jsonl"], cwd=run_dir, stdout=output).returncode != 0:
            raise ExportError("audit snapshot verification failed")
    if artifact.read_bytes() != restored.read_bytes():
        raise ExportError("audit snapshot verification failed")
    return sid


def parse_args(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--audit-log", required=True, type=Path)
    p.add_argument("--checkpoint", required=True, type=Path)
    p.add_argument("--work-root", required=True, type=Path)
    p.add_argument("--restic-binary", required=True, type=Path)
    p.add_argument("--environment", required=True)
    p.add_argument("--max-records", type=int, default=1000)
    a = p.parse_args(argv)
    if not ENV_RE.fullmatch(a.environment) or a.max_records <= 0 or any(not x.is_absolute() for x in (a.audit_log, a.checkpoint, a.work_root, a.restic_binary)):
        p.error("invalid path or environment")
    return a


def main(argv=None) -> int:
    os.umask(0o077)
    try:
        a = parse_args(argv)
        private_file(a.audit_log, "audit log")
        if a.checkpoint.exists(): private_file(a.checkpoint, "checkpoint", writable=True)
        if not a.work_root.is_dir() or a.work_root.stat().st_uid != os.geteuid() or a.work_root.stat().st_mode & 0o077:
            raise ExportError("invalid work root")
        executable_file(a.restic_binary)
        restic_env()
        lock_path = a.checkpoint.with_suffix(a.checkpoint.suffix + ".lock")
        lock_path.touch(mode=0o600, exist_ok=True)
        with lock_path.open("r+") as lock:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
            return _run_locked(a)
    except (ExportError, OSError, ValueError, UnicodeError, json.JSONDecodeError):
        print("audit export failed", file=sys.stderr)
        return 1


def _run_locked(a) -> int:
    try:
        offset, _ = load_checkpoint(a.checkpoint, a.audit_log)
        source_stat = a.audit_log.stat()
        if offset > source_stat.st_size: raise ExportError("audit source rotated or checkpoint invalid")
        with a.audit_log.open("rb") as source:
            source.seek(offset)
            payload = []
            end = offset
            while True:
                line = source.readline()
                if not line: break
                if not line.endswith(b"\n"):
                    end = source.tell() - len(line)
                    break
                item = json.loads(line)
                # The server's JSON logger also contains grpc request and
                # other operational records. Only the dedicated audit event
                # is exported; unrelated events are never copied verbatim.
                if isinstance(item, dict) and item.get("msg") != "mutation audit":
                    end = source.tell()
                    continue
                payload.append(safe_record(item, len(payload) + 1))
                end = source.tell()
                if len(payload) >= a.max_records:
                    end = source.tell()
                    break
        if not payload:
            print("audit export up to date")
            return 0
        prefix_fingerprint = source_fingerprint(a.audit_log, end)
        with tempfile.TemporaryDirectory(prefix="audit-export.", dir=a.work_root) as temp:
            run_dir = Path(temp)
            artifact = run_dir / "audit.jsonl"
            artifact.write_text("".join(json.dumps(item, sort_keys=True, separators=(",", ":")) + "\n" for item in payload), encoding="utf-8")
            sid = upload_verify(a.restic_binary, artifact, a.environment, run_dir, end)
        if source_fingerprint(a.audit_log, end) != prefix_fingerprint:
            raise ExportError("audit source changed during export")
        data = {"offset": end, "device": source_stat.st_dev, "inode": source_stat.st_ino, "fingerprint": prefix_fingerprint}
        temporary = a.checkpoint.with_suffix(a.checkpoint.suffix + ".tmp")
        temporary.write_text(json.dumps(data, sort_keys=True) + "\n", encoding="utf-8")
        os.chmod(temporary, 0o600)
        os.replace(temporary, a.checkpoint)
        print(f"audit export verified: environment={a.environment} snapshot={sid} records={len(payload)}")
        return 0
    except (ExportError, OSError, ValueError, UnicodeError, json.JSONDecodeError):
        raise


if __name__ == "__main__":
    raise SystemExit(main())
