#!/usr/bin/env python3
"""Repository-only checks for the backup metrics exporter and alert rules."""

from http.client import HTTPConnection
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import shutil


ROOT = Path(__file__).resolve().parent.parent
EXPORTER = ROOT / "scripts/serve-backup-metrics.py"
UNIT = ROOT / "deploy/backup/deployer-backup-metrics.service"
RULES = ROOT / "deploy/backup/prometheus-backup-rules.yml"
RULE_TESTS = ROOT / "tests/backup-metrics-rules-test.yml"
RECOVERY_RULES = ROOT / "deploy/backup/prometheus-recovery-backup-rules.yml"
RECOVERY_RULE_TESTS = ROOT / "tests/recovery-backup-metrics-rules-test.yml"
METRICS = """# HELP deployer_platform_backup_last_success_timestamp_seconds Unix timestamp of the last successful platform backup.
# TYPE deployer_platform_backup_last_success_timestamp_seconds gauge
deployer_platform_backup_last_success_timestamp_seconds 1720000000
deployer_platform_backup_last_run_success 1
deployer_platform_backup_last_attempt_timestamp_seconds 1720000001
deployer_platform_backup_duration_seconds 12.5
"""


def request(port, path):
    connection = HTTPConnection("127.0.0.1", port, timeout=3)
    try:
        connection.request("GET", path)
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


with tempfile.TemporaryDirectory(prefix="backup-metrics-test-") as directory:
    metrics_file = Path(directory) / "metrics.prom"
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        port = probe.getsockname()[1]
    process = subprocess.Popen(
        [sys.executable, str(EXPORTER), "--metrics-file", str(metrics_file),
         "--listen", "127.0.0.1", "--port", str(port)],
        cwd=ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        for _ in range(50):
            try:
                status, _ = request(port, "/metrics")
                break
            except OSError:
                time.sleep(0.02)
        else:
            raise AssertionError("exporter did not start")
        assert status == 503, status

        metrics_file.write_text(METRICS, encoding="utf-8")
        status, body = request(port, "/metrics")
        assert status == 200, status
        assert body.decode("utf-8") == METRICS

        status, _ = request(port, "/secret")
        assert status == 404, status
        status, _ = request(port, "/%2e%2e/metrics")
        assert status == 404, status

        metrics_file.write_bytes(b"not prometheus exposition\n")
        status, _ = request(port, "/metrics")
        assert status == 503, status
        metrics_file.write_bytes(b"x" * (64 * 1024 + 1))
        status, _ = request(port, "/metrics")
        assert status == 503, status
    finally:
        process.terminate()
        process.wait(timeout=3)
        assert process.stdout.read() == ""
        assert process.stderr.read() == ""

assert "--listen 10.8.0.1 --port 9708" in UNIT.read_text()
assert "/usr/local/libexec/serve-backup-metrics.py" in UNIT.read_text()
assert "/var/lib/deployer/backup-status/platform-backup.prom" in UNIT.read_text()
unit = UNIT.read_text()
for hardening in ("NoNewPrivileges=true", "ProtectSystem=strict", "ProtectHome=true", "PrivateTmp=true", "ReadOnlyPaths=/var/lib/deployer/backup-status"):
    assert hardening in unit, hardening
rules = RULES.read_text()
for expression in (
    'deployer_platform_backup_last_run_success{job="deployer-platform-backup"} == 0',
    'time() - deployer_platform_backup_last_success_timestamp_seconds{job="deployer-platform-backup"} > 36 * 60 * 60',
    'absent(deployer_platform_backup_last_run_success{job="deployer-platform-backup"})',
    'absent(deployer_platform_backup_last_success_timestamp_seconds{job="deployer-platform-backup"})',
):
    assert expression in rules, expression
assert rules.count("for: 5m") == 3

with tempfile.TemporaryDirectory(prefix="backup-metrics-rules-test-") as directory:
    checks = Path(directory)
    for rules_path, tests_path in ((RULES, RULE_TESTS), (RECOVERY_RULES, RECOVERY_RULE_TESTS)):
        rules_name = rules_path.name
        tests_name = tests_path.name
        shutil.copy2(rules_path, checks / rules_name)
        shutil.copy2(tests_path, checks / tests_name)
        subprocess.run(
            ["docker", "run", "--rm", "--network", "none",
             "--user", f"{os.getuid()}:{os.getgid()}",
             "--entrypoint", "/bin/promtool", "-v", f"{checks}:/checks:ro",
             "prom/prometheus:v3.13.0", "test", "rules", f"/checks/{tests_name}"],
            check=True,
            cwd=ROOT,
        )
print("backup metrics exporter, unit, and alert rule tests passed")
