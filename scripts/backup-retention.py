#!/usr/bin/env python3
"""Plan or explicitly apply narrow restic backup retention.

The default is a read-only plan.  Applying deletes only the explicit snapshot
IDs selected under one environment and one backup-kind tag.  Pruning is a
separate explicit switch.
"""
from __future__ import annotations
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ENV_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$")
ID_RE = re.compile(r"^[0-9a-f]{64}$")

class RetentionError(Exception): pass

def private(path: Path, label: str):
    try: st = path.lstat()
    except OSError as exc: raise RetentionError(f"invalid {label}") from exc
    if path.is_symlink() or not path.is_file() or st.st_uid != os.geteuid() or st.st_mode & 0o077:
        raise RetentionError(f"invalid {label}")

def executable(path: Path):
    try: st = path.lstat()
    except OSError as exc: raise RetentionError("invalid restic binary") from exc
    if path.is_symlink() or not path.is_file() or st.st_uid != os.geteuid() or st.st_mode & 0o022 or not os.access(path, os.X_OK):
        raise RetentionError("invalid restic binary")

def run(restic: Path, args: list[str], cwd: Path) -> subprocess.CompletedProcess:
    try: return subprocess.run([str(restic), "--no-cache", *args], cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    except OSError as exc: raise RetentionError("restic operation failed") from exc

def tags_for(kind: str, tags: object) -> bool:
    return isinstance(tags, list) and kind in tags or isinstance(tags, list) and f"backup-kind:{kind}" in tags

def parse_time(value: object) -> dt.datetime:
    if not isinstance(value, str): raise RetentionError("invalid snapshot time")
    try: return dt.datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(dt.timezone.utc)
    except ValueError as exc: raise RetentionError("invalid snapshot time") from exc

def selection(items: list[dict], now: dt.datetime, daily: int, weekly: int, monthly: int) -> tuple[list[dict], list[dict]]:
    ordered = sorted(items, key=lambda x: (parse_time(x["time"]), x["id"]), reverse=True)
    keep: set[str] = set()
    days: set[tuple[int,int,int]] = set(); weeks: set[tuple[int,int]] = set(); months: set[tuple[int,int]] = set()
    for item in ordered:
        stamp = parse_time(item["time"])
        # pre-upgrade and rollback artifacts are recovery points, never automatic candidates.
        tags = item.get("tags", [])
        if any(isinstance(tag, str) and (tag == "pre-upgrade" or tag.startswith("pre-upgrade-") or tag == "rollback" or tag.startswith("rollback-")) for tag in tags):
            keep.add(item["id"]); continue
        if stamp > now: # future-dated snapshots are retained and never deleted.
            keep.add(item["id"]); continue
        day = (stamp.year, stamp.month, stamp.day)
        year, week, _ = stamp.isocalendar()
        month = (stamp.year, stamp.month)
        if day not in days and len(days) < daily: days.add(day); keep.add(item["id"])
        if (year, week) not in weeks and len(weeks) < weekly: weeks.add((year, week)); keep.add(item["id"])
        if month not in months and len(months) < monthly: months.add(month); keep.add(item["id"])
    return [x for x in ordered if x["id"] in keep], [x for x in ordered if x["id"] not in keep]

def audit_selection(items, now, days):
    cutoff = now - dt.timedelta(days=days)
    kept, eligible = [], []
    for item in items:
        protected = any(isinstance(tag, str) and (tag == "pre-upgrade" or tag.startswith("pre-upgrade-") or tag == "rollback" or tag.startswith("rollback-")) for tag in item.get("tags", []))
        (kept if protected or parse_time(item["time"]) >= cutoff else eligible).append(item)
    return kept, eligible

def main(argv=None) -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--restic-binary", required=True, type=Path)
    p.add_argument("--environment", required=True)
    p.add_argument("--backup-kind", required=True)
    p.add_argument("--daily", type=int)
    p.add_argument("--weekly", type=int)
    p.add_argument("--monthly", type=int)
    p.add_argument("--audit-keep-days", type=int, help="retain every audit batch uploaded within this many days; never thin by calendar buckets")
    p.add_argument("--now", default=None, help="UTC RFC3339 time, for deterministic planning")
    p.add_argument("--apply", action="store_true")
    p.add_argument("--prune", action="store_true", help="also prune unreferenced data after an applied deletion")
    a = p.parse_args(argv)
    try:
        if not ENV_RE.fullmatch(a.environment) or not ENV_RE.fullmatch(a.backup_kind) or a.backup_kind == "audit-export":
            raise RetentionError("environment, backup kind, and retention counts are invalid")
        if a.backup_kind == "audit":
            if not a.audit_keep_days or a.audit_keep_days <= 0 or any(x is not None for x in (a.daily, a.weekly, a.monthly)):
                raise RetentionError("audit retention requires only --audit-keep-days")
        elif a.audit_keep_days is not None or any(x is None or x <= 0 for x in (a.daily, a.weekly, a.monthly)):
            raise RetentionError("backup retention requires positive daily, weekly and monthly counts")
        now = parse_time(a.now) if a.now else dt.datetime.now(dt.timezone.utc)
        executable(a.restic_binary)
        if not os.environ.get("RESTIC_REPOSITORY") or not os.environ.get("RESTIC_PASSWORD_FILE"):
            raise RetentionError("RESTIC_REPOSITORY and RESTIC_PASSWORD_FILE are required")
        if any(x in os.environ for x in ("RESTIC_PASSWORD", "RESTIC_PASSWORD_COMMAND", "RESTIC_REPOSITORY_FILE")):
            raise RetentionError("restic password alternatives are not allowed")
        private(Path(os.environ["RESTIC_PASSWORD_FILE"]), "restic password file")
        result = run(a.restic_binary, ["snapshots", "--json"], Path.cwd())
        if result.returncode != 0:
            raise RetentionError("cannot list snapshots")
        snapshots = json.loads(result.stdout.decode("utf-8"))
        if not isinstance(snapshots, list): raise RetentionError("invalid snapshot listing")
        scoped = []
        for item in snapshots:
            if not isinstance(item, dict) or not isinstance(item.get("id"), str) or not ID_RE.fullmatch(item["id"]): continue
            tags = item.get("tags")
            if isinstance(tags, list) and a.environment in tags and tags_for(a.backup_kind, tags): scoped.append(item)
        if a.backup_kind == "audit":
            kept, eligible = audit_selection(scoped, now, a.audit_keep_days)
        else:
            kept, eligible = selection(scoped, now, a.daily, a.weekly, a.monthly)
        print(json.dumps({"environment": a.environment, "backup_kind": a.backup_kind, "dry_run": not a.apply, "keep": [x["id"] for x in kept], "eligible": [x["id"] for x in eligible]}, sort_keys=True))
        if a.prune and not a.apply: raise RetentionError("--prune requires --apply")
        if a.apply and eligible:
            ids = [x["id"] for x in eligible]
            if run(a.restic_binary, ["forget", *ids], Path.cwd()).returncode != 0: raise RetentionError("retention deletion failed")
        if a.apply and a.prune and run(a.restic_binary, ["prune"], Path.cwd()).returncode != 0: raise RetentionError("retention prune failed")
        return 0
    except (RetentionError, OSError, ValueError, KeyError, TypeError, json.JSONDecodeError):
        print("backup retention failed", file=sys.stderr); return 1

if __name__ == "__main__": raise SystemExit(main())
