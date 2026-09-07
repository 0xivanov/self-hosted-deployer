#!/usr/bin/env python3
"""Standalone tests for atomic PostgreSQL globals capture."""

import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/backup-postgres-globals.py"


class PostgresGlobalsBackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="postgres-globals-test-")
        self.root = Path(self.temp.name)
        self.output_dir = self.root / "output"
        self.output_dir.mkdir(mode=0o700)
        self.output = self.output_dir / "postgres-globals.sql"
        self.binary = self.root / "pg_dumpall"
        self.binary.write_text(
            "#!/usr/bin/env python3\n"
            "import os, sys\n"
            "assert sys.argv[1:] == ['--username=postgres', '--globals-only']\n"
            "if os.environ.get('MODE') == 'empty': raise SystemExit(0)\n"
            "if os.environ.get('MODE') == 'fail': raise SystemExit(1)\n"
            "sys.stdout.write('CREATE ROLE backup;\\n')\n"
        )
        self.binary.chmod(0o755)

    def tearDown(self):
        self.temp.cleanup()

    def run_capture(self, mode=None):
        env = os.environ.copy()
        if mode:
            env["MODE"] = mode
        return subprocess.run(
            [sys.executable, str(SCRIPT), "--pg-dumpall", str(self.binary), "--output", str(self.output)],
            env=env, capture_output=True, text=True,
        )

    def test_success_replaces_atomically_with_private_output(self):
        result = self.run_capture()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.output.read_text(), "CREATE ROLE backup;\n")
        self.assertEqual(stat.S_IMODE(self.output.stat().st_mode), 0o600)
        self.assertFalse(list(self.output_dir.glob(".postgres-globals.sql.*")))

    def test_failure_and_empty_preserve_existing_output(self):
        self.output.write_text("previous\n")
        self.output.chmod(0o600)
        for mode in ("fail", "empty"):
            result = self.run_capture(mode)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(self.output.read_text(), "previous\n")
            self.assertFalse(list(self.output_dir.glob(".postgres-globals.sql.*")))

    def test_invalid_parent_is_rejected_without_output(self):
        self.output_dir.chmod(0o755)
        result = self.run_capture()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
