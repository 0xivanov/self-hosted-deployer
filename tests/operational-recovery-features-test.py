#!/usr/bin/env python3
"""Small deterministic checks for the operational recovery helpers."""
import datetime as dt
import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

retention = load("retention", "backup-retention.py")
audit = load("audit", "offsite-audit-export.py")

def test_retention_scope_and_rollback():
    now = dt.datetime(2026, 9, 7, tzinfo=dt.timezone.utc)
    snapshots = [
        {"id": "a" * 64, "time": "2026-09-06T10:00:00Z", "tags": ["prod", "recovery"]},
        {"id": "b" * 64, "time": "2026-09-05T10:00:00Z", "tags": ["prod", "recovery", "rollback"]},
        {"id": "c" * 64, "time": "2026-09-04T10:00:00Z", "tags": ["other", "recovery"]},
    ]
    kept, eligible = retention.selection(snapshots[:2], now, 1, 1, 1)
    assert {item["id"] for item in kept} == {"a" * 64, "b" * 64}
    assert not eligible
    assert retention.tags_for("recovery", ["prod", "recovery"])
    assert not retention.tags_for("recovery", ["prod", "platform"])

def test_audit_allowlist_rejects_arbitrary_payload():
    safe = audit.safe_record({"level": "INFO", "msg": "mutation audit", "timestamp": "2026-09-07T00:00:00Z", "method": "/deployer.v1.AppService/DeleteApp", "outcome": "OK", "correlation_id": "a" * 32, "token_id": "b" * 32, "caller_kind": "admin", "target": {"app": "reports"}, "component": "server"}, 1)
    assert safe["target"] == {"app": "reports"}
    try:
        audit.safe_record({"timestamp": "x", "method": "/x", "request": "secret"}, 2)
    except audit.ExportError:
        return
    raise AssertionError("arbitrary audit payload accepted")

test_retention_scope_and_rollback()
test_audit_allowlist_rejects_arbitrary_payload()
print("operational recovery feature tests passed")

# Every recent incremental batch must survive, even when uploaded on the same day.
now = dt.datetime(2026, 9, 8, tzinfo=dt.timezone.utc)
items = [{"id": str(i), "time": stamp, "tags": ["pilot", "audit"]} for i, stamp in enumerate(["2026-09-07T01:00:00Z", "2026-09-07T02:00:00Z", "2026-01-01T00:00:00Z"])]
kept, eligible = retention.audit_selection(items, now, 90)
assert [x["id"] for x in kept] == ["0", "1"]
assert [x["id"] for x in eligible] == ["2"]
