#!/usr/bin/env python3
"""Focused tests for offline recovery bundle staging."""

import hashlib
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "stage-recovery-bundle.py"


class StageRecoveryBundleTests(unittest.TestCase):
    def setUp(self):
        temp_base = "/private/tmp" if Path("/private/tmp").is_dir() else None
        self.temp = tempfile.TemporaryDirectory(dir=temp_base)
        self.root = Path(self.temp.name)
        self.parent = self.root / "staging"
        self.parent.mkdir(mode=0o700)
        self.archive = self.root / "recovery.tar.gz"

    def tearDown(self):
        self.temp.cleanup()

    def make_archive(self, members):
        with tarfile.open(self.archive, "w:gz") as archive:
            for name, kind, data, mode in members:
                info = tarfile.TarInfo(name)
                info.mode = mode
                if kind == "dir":
                    info.type = tarfile.DIRTYPE
                    archive.addfile(info)
                elif kind == "file":
                    payload = data.encode() if isinstance(data, str) else data
                    info.size = len(payload)
                    archive.addfile(info, __import__("io").BytesIO(payload))
                else:
                    info.type = kind
                    archive.addfile(info)

    def run_stage(self, output=None, digest=None, archive=None):
        archive = archive or self.archive
        output = output or self.parent / "result"
        digest = digest or hashlib.sha256(archive.read_bytes()).hexdigest()
        return subprocess.run(
            ["python3", str(SCRIPT), str(archive), "--sha256", digest, "--output", str(output)],
            capture_output=True, text=True,
        )

    def test_valid_archive_preserves_content_and_sanitized_permissions(self):
        self.make_archive([
            ("etc", "dir", b"", 0o755),
            ("etc/server.env", "file", "secret=synthetic\n", 0o6750),
            ("bin/tool", "file", b"#!/bin/sh\n", 0o755),
            ("late/child", "file", b"data", 0o600),
            ("late", "dir", b"", 0o750),
        ])
        result = self.run_stage()
        self.assertEqual(result.returncode, 0, result.stderr)
        output = self.parent / "result"
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)
        self.assertEqual((output / "etc").stat().st_mode & 0o777, 0o755)
        self.assertEqual((output / "etc/server.env").read_text(), "secret=synthetic\n")
        self.assertEqual((output / "etc/server.env").stat().st_mode & 0o7777, 0o750)
        self.assertEqual((output / "bin/tool").stat().st_mode & 0o777, 0o755)
        self.assertEqual((output / "late").stat().st_mode & 0o777, 0o750)

    def test_checksum_mismatch_writes_nothing(self):
        self.make_archive([("file", "file", b"data", 0o600)])
        result = self.run_stage(digest="0" * 64)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.parent / "result").exists())

    def test_rejects_traversal_absolute_links_and_duplicates(self):
        for index, members in enumerate([
            [("../escape", "file", b"x", 0o600)],
            [("/absolute", "file", b"x", 0o600)],
            [("link", tarfile.SYMTYPE, b"", 0o777)],
            [("link", tarfile.LNKTYPE, b"", 0o777)],
            [("fifo", tarfile.FIFOTYPE, b"", 0o600)],
            [("file/child", "file", b"x", 0o600), ("file", "file", b"y", 0o600)],
            [("same", "file", b"x", 0o600), ("same", "file", b"y", 0o600)],
            [("file", "file", b"x", 0o600), ("file/child", "file", b"y", 0o600)],
        ]):
            self.make_archive(members)
            output = self.parent / f"result-{index}"
            result = self.run_stage(output=output)
            self.assertNotEqual(result.returncode, 0, members)
            self.assertFalse(output.exists(), members)

    def test_rejects_existing_output_and_symlinked_parent(self):
        self.make_archive([("file", "file", b"x", 0o600)])
        existing = self.parent / "existing"
        existing.mkdir(mode=0o700)
        (existing / "sentinel").write_text("keep")
        self.assertNotEqual(self.run_stage(output=existing).returncode, 0)
        self.assertEqual((existing / "sentinel").read_text(), "keep")
        real = self.root / "real"
        real.mkdir(mode=0o700)
        link = self.root / "link"
        link.symlink_to(real, target_is_directory=True)
        self.assertNotEqual(self.run_stage(output=link / "result").returncode, 0)


if __name__ == "__main__":
    unittest.main()
