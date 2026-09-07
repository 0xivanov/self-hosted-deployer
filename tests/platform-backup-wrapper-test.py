#!/usr/bin/env python3
"""Standalone unittest coverage for the platform backup scheduling wrapper."""

import os
from pathlib import Path
import signal
import stat
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parent.parent
WRAPPER = ROOT / "scripts/deployer-platform-backup.py"


class PlatformBackupWrapperTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory(prefix="platform-backup-wrapper-test-")
        self.root = Path(self.tempdir.name)
        self.state = self.root / "state"
        self.command = self.root / "mock-backup.py"
        self.command.write_text(
            "#!/usr/bin/env python3\n"
            "import os, pathlib, sys, time\n"
            "pathlib.Path(os.environ['PID_FILE']).write_text(str(os.getpid()))\n"
            "pathlib.Path(os.environ['ARGUMENTS']).write_text('\\0'.join(sys.argv[1:]))\n"
            "if os.environ.get('SLEEP'): time.sleep(float(os.environ['SLEEP']))\n"
            "raise SystemExit(int(os.environ.get('EXIT_CODE', '0')))\n"
        )
        self.command.chmod(0o700)

    def tearDown(self):
        self.tempdir.cleanup()

    def invoke(self, *args, env=None):
        child_env = os.environ.copy()
        child_env.update({"ARGUMENTS": str(self.root / "arguments"), "PID_FILE": str(self.root / "pid")})
        if env:
            child_env.update(env)
        return subprocess.run(
            [sys.executable, str(WRAPPER), "--state-directory", str(self.state), "--", str(self.command), *args],
            env=child_env,
            capture_output=True,
            text=True,
        )

    def metrics(self):
        values = {}
        for line in (self.state / "platform-backup.prom").read_text().splitlines():
            if line and not line.startswith("#"):
                name, value = line.split(" ", 1)
                values[name] = value
        return values

    def test_success_writes_private_state_metrics_and_preserves_arguments(self):
        result = self.invoke("--database-path", "/tmp/path with spaces", env={"EXIT_CODE": "0"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / "arguments").read_text(), "--database-path\0/tmp/path with spaces")
        values = self.metrics()
        self.assertEqual(values["deployer_platform_backup_last_run_success"], "1")
        self.assertEqual(values["deployer_platform_backup_last_success_timestamp_seconds"], values["deployer_platform_backup_last_attempt_timestamp_seconds"])
        self.assertGreaterEqual(float(values["deployer_platform_backup_duration_seconds"]), 0)
        self.assertEqual(stat.S_IMODE(self.state.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE((self.state / "platform-backup.prom").stat().st_mode), 0o644)
        self.assertFalse(list(self.state.glob(".platform-backup.prom.*")))

    def test_failure_marks_run_failed_and_preserves_last_success(self):
        self.assertEqual(self.invoke().returncode, 0)
        first = self.metrics()["deployer_platform_backup_last_success_timestamp_seconds"]
        result = self.invoke(env={"EXIT_CODE": "7"})
        self.assertEqual(result.returncode, 1)
        self.assertIn("platform backup command failed", result.stderr)
        values = self.metrics()
        self.assertEqual(values["deployer_platform_backup_last_run_success"], "0")
        self.assertEqual(values["deployer_platform_backup_last_success_timestamp_seconds"], first)
        self.assertGreaterEqual(float(values["deployer_platform_backup_last_attempt_timestamp_seconds"]), float(first))

    def test_nonblocking_lock_skips_without_replacing_metrics(self):
        self.assertEqual(self.invoke().returncode, 0)
        before = (self.state / "platform-backup.prom").read_bytes()
        lock = open(self.state / ".platform-backup.lock", "a+")
        try:
            import fcntl
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
            result = self.invoke(env={"EXIT_CODE": "7"})
        finally:
            lock.close()
        self.assertEqual(result.returncode, 0)
        self.assertIn("already running", result.stderr)
        self.assertEqual((self.state / "platform-backup.prom").read_bytes(), before)

    def test_interrupt_publishes_failed_attempt(self):
        child_env = os.environ.copy()
        child_env.update({"ARGUMENTS": str(self.root / "arguments"), "PID_FILE": str(self.root / "pid"), "SLEEP": "10"})
        process = subprocess.Popen(
            [sys.executable, str(WRAPPER), "--state-directory", str(self.state), "--", str(self.command)],
            env=child_env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        try:
            time.sleep(0.1)
            process.send_signal(signal.SIGTERM)
            stdout, stderr = process.communicate(timeout=3)
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate()
        self.assertEqual(process.returncode, 130, (stdout, stderr))
        child_pid = int((self.root / "pid").read_text())
        with self.assertRaises(ProcessLookupError):
            os.kill(child_pid, 0)
        values = self.metrics()
        self.assertEqual(values["deployer_platform_backup_last_run_success"], "0")
        self.assertIn("deployer_platform_backup_last_attempt_timestamp_seconds", values)


if __name__ == "__main__":
    unittest.main()
