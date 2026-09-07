#!/usr/bin/env python3
"""Isolated tests for the recovery bundle command."""

import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import signal
import tarfile
import tempfile
import time
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "backup-recovery-offsite.py"


class RecoveryBundleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.work = self.root / "work"
        self.work.mkdir(mode=0o700)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.password = self.root / "password"
        self.password.write_text("test-password\n")
        os.chmod(self.password, 0o600)
        self.restic = self.root / "restic"
        self.restic.write_text(
            "#!/usr/bin/env python3\n"
            "import json, os, sys\n"
            "repo=os.environ['RESTIC_REPOSITORY']\n"
            "command=sys.argv[2] if sys.argv[1] == '--no-cache' else sys.argv[1]\n"
            "if command == 'backup':\n"
            "  data=sys.stdin.buffer.read(); open(os.path.join(repo, 'recovery.tar.gz'),'wb').write(data)\n"
            "  print(json.dumps({'message_type':'summary','snapshot_id':'0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'}))\n"
            "elif command == 'dump':\n"
            "  sys.stdout.buffer.write(open(os.path.join(repo, 'recovery.tar.gz'),'rb').read())\n"
        )
        os.chmod(self.restic, 0o755)
        self.source_dir = self.root / "etc" / "deployer"
        self.source_dir.mkdir(parents=True)
        self.secret_dir = self.source_dir / "backup"
        self.secret_dir.mkdir()
        (self.source_dir / "server.env").write_text("DEPLOYER_PUBLIC_BASE_URL=https://example.test\n")
        (self.secret_dir / "credentials").write_text("private\n")
        self.db = self.source_dir / "deployer.db"
        connection = sqlite3.connect(self.db)
        connection.execute("create table state (value text)")
        connection.execute("insert into state values ('snapshot')")
        connection.commit()
        connection.close()
        self.config = self.root / "config.json"
        self.config.write_text(json.dumps({
            "sqlite_databases": [{"source": str(self.db), "archive_path": "/var/lib/deployer/deployer.db"}],
            "paths": [str(self.source_dir)],
            "excluded_paths": [str(self.secret_dir)],
            "required_files": [str(self.source_dir / "server.env")],
        }))
        os.chmod(self.config, 0o600)

    def tearDown(self):
        self.temp.cleanup()

    def run_bundle(self, config=None, restic=None):
        environment = os.environ.copy()
        environment.update(RESTIC_REPOSITORY=str(self.repo), RESTIC_PASSWORD_FILE=str(self.password))
        return subprocess.run(
            ["python3", str(SCRIPT), "--config", str(config or self.config), "--work-root", str(self.work), "--restic-binary", str(restic or self.restic), "--environment", "test-vps"],
            env=environment, capture_output=True, text=True,
        )

    def write_config(self, values, name="variant.json"):
        path = self.root / name
        path.write_text(json.dumps(values))
        os.chmod(path, 0o600)
        return path

    def test_sqlite_snapshot_and_tar_boundaries(self):
        wal_connection = sqlite3.connect(self.db)
        wal_connection.execute("pragma journal_mode=wal")
        wal_connection.execute("pragma wal_autocheckpoint=0")
        wal_connection.execute("insert into state values ('wal-committed')")
        wal_connection.commit()
        self.assertTrue(Path(str(self.db) + "-wal").exists())
        result = self.run_bundle()
        wal_connection.close()
        self.assertEqual(result.returncode, 0, result.stderr)
        with tarfile.open(self.repo / "recovery.tar.gz", "r:gz") as archive:
            names = archive.getnames()
            self.assertIn("var/lib/deployer/deployer.db", names)
            source_prefix = self.source_dir.as_posix().lstrip("/")
            self.assertIn(f"{source_prefix}/server.env", names)
            self.assertNotIn(f"{source_prefix}/backup/credentials", names)
            self.assertEqual(sum(name.endswith("/deployer.db") for name in names), 1)
            self.assertNotIn(f"{source_prefix}/deployer.db", names)
            self.assertNotIn(f"{source_prefix}/deployer.db-wal", names)
            self.assertNotIn(f"{source_prefix}/deployer.db-shm", names)
            recovered = self.root / "recovered.db"
            recovered.write_bytes(archive.extractfile("var/lib/deployer/deployer.db").read())
        snapshot = sqlite3.connect(recovered)
        self.assertEqual(snapshot.execute("pragma integrity_check").fetchone(), ("ok",))
        self.assertEqual(snapshot.execute("select value from state order by rowid desc").fetchone(), ("wal-committed",))
        snapshot.close()

    def test_fresh_postgres_dump_and_globals_are_archived(self):
        dump_dir = self.root / "dumps"
        dump_dir.mkdir()
        dump = dump_dir / "money-manager-2026.dump"
        dump.write_bytes(b"dump")
        tools = self.root / "postgres-tools"
        tools.write_text(
            "#!/usr/bin/env python3\n"
            "import sys\n"
            "if '--list' in sys.argv: raise SystemExit(0)\n"
            "if '--globals' in sys.argv: print('CREATE ROLE recovery;')\n"
        )
        os.chmod(tools, 0o755)
        config = json.loads(self.config.read_text())
        config.update(postgres_dump_directory=str(dump_dir), pg_restore_binary=str(tools), postgres_globals_command=[str(tools), "--globals"])
        result = self.run_bundle(self.write_config(config))
        self.assertEqual(result.returncode, 0, result.stderr)
        with tarfile.open(self.repo / "recovery.tar.gz", "r:gz") as archive:
            self.assertIn("postgres-globals.sql", archive.getnames())
            self.assertEqual(archive.extractfile("postgres-globals.sql").read(), b"CREATE ROLE recovery;\n")
            self.assertIn(dump.as_posix().lstrip("/"), archive.getnames())

    def test_empty_globals_and_invalid_dump_are_rejected(self):
        dump_dir = self.root / "dumps"
        dump_dir.mkdir()
        dump = dump_dir / "money-manager-current.dump"
        dump.write_bytes(b"dump")
        tools = self.root / "empty-tools"
        tools.write_text("#!/bin/sh\n[ \"$1\" = --list ]\n")
        os.chmod(tools, 0o755)
        config = json.loads(self.config.read_text())
        config.update(postgres_dump_directory=str(dump_dir), pg_restore_binary=str(tools), postgres_globals_command=[str(tools), "--globals"])
        result = self.run_bundle(self.write_config(config, "empty.json"))
        self.assertNotEqual(result.returncode, 0)

    def test_required_file_mutation_during_capture_is_rejected(self):
        tool = self.root / "mutating-tool"
        required = self.source_dir / "server.env"
        tool.write_text("#!/usr/bin/env python3\nimport pathlib,sys\np=pathlib.Path(sys.argv[1]); p.write_text('rotated\\n'); print('globals')\n")
        os.chmod(tool, 0o755)
        config = json.loads(self.config.read_text())
        config["postgres_globals_command"] = [str(tool), str(required)]
        result = self.run_bundle(self.write_config(config, "mutating.json"))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.repo / "recovery.tar.gz").exists())

    def test_config_conflicts_are_rejected(self):
        config = json.loads(self.config.read_text())
        config["required_files"] = [str(self.secret_dir / "credentials")]
        self.assertNotEqual(self.run_bundle(self.write_config(config, "excluded-required.json")).returncode, 0)
        config = json.loads(self.config.read_text())
        config["paths"].append(str(self.work))
        self.assertNotEqual(self.run_bundle(self.write_config(config, "work-conflict.json")).returncode, 0)

    def test_sigterm_removes_private_staging(self):
        sleeper = self.root / "sleeping-tools"
        sleeper.write_text("#!/bin/sh\nsleep 30\n")
        os.chmod(sleeper, 0o755)
        config = json.loads(self.config.read_text())
        config["postgres_globals_command"] = [str(sleeper)]
        config_path = self.write_config(config, "interrupt.json")
        env = os.environ.copy()
        env.update(RESTIC_REPOSITORY=str(self.repo), RESTIC_PASSWORD_FILE=str(self.password))
        process = subprocess.Popen(["python3", str(SCRIPT), "--config", str(config_path), "--work-root", str(self.work), "--restic-binary", str(self.restic), "--environment", "test-vps"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(0.15)
        process.send_signal(signal.SIGTERM)
        process.wait(timeout=5)
        self.assertEqual(process.returncode, 130)
        self.assertEqual(list(self.work.iterdir()), [])

    def test_stale_postgres_dump_is_rejected(self):
        dump_dir = self.root / "dumps"
        dump_dir.mkdir()
        dump = dump_dir / "money-manager-old.dump"
        dump.write_bytes(b"dump")
        old = dump.stat().st_mtime - 37 * 3600
        os.utime(dump, (old, old))
        config = json.loads(self.config.read_text())
        config.update(postgres_dump_directory=str(dump_dir), pg_restore_binary=str(self.restic))
        path = self.root / "stale.json"
        path.write_text(json.dumps(config))
        os.chmod(path, 0o600)
        result = self.run_bundle(path)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.repo / "recovery.tar.gz").exists())

    def test_upload_failure_has_no_success(self):
        failing = self.root / "failing-restic"
        failing.write_text("#!/bin/sh\nexit 1\n")
        os.chmod(failing, 0o755)
        result = self.run_bundle(restic=failing)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("verified", result.stdout)

    def test_round_trip_mismatch_is_rejected(self):
        mismatch = self.root / "mismatch-restic"
        mismatch.write_text(
            "#!/usr/bin/env python3\n"
            "import json, os, sys\n"
            "repo=os.environ['RESTIC_REPOSITORY']\n"
            "command=sys.argv[2] if sys.argv[1] == '--no-cache' else sys.argv[1]\n"
            "if command == 'backup':\n"
            " open(os.path.join(repo,'recovery.tar.gz'),'wb').write(sys.stdin.buffer.read())\n"
            " print(json.dumps({'message_type':'summary','snapshot_id':'0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'}))\n"
            "else:\n"
            " sys.stdout.buffer.write(b'changed')\n"
        )
        os.chmod(mismatch, 0o755)
        result = self.run_bundle(restic=mismatch)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("verified", result.stdout)


if __name__ == "__main__":
    unittest.main()
