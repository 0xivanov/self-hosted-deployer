#!/usr/bin/env python3
"""Create and verify a daily, restic-backed VPS recovery bundle.

The command intentionally only reads live inputs.  SQLite databases are copied
with SQLite's online backup API, and the resulting tar stream is uploaded to an
already-initialized restic repository.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import sqlite3
import stat
import subprocess
import sys
import signal
import tarfile
import tempfile
import time
from urllib.parse import quote


MAX_DUMP_AGE_HOURS = 36
SNAPSHOT_ID_RE = re.compile(r"^[0-9a-f]{64}$")
ENVIRONMENT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$")


class BundleError(Exception):
    """An expected, user-facing bundle failure."""


class BundleInterrupted(Exception):
    """A termination request that must pass through cleanup."""


def handle_interrupt(_number, _frame):
    raise BundleInterrupted()


def _absolute(value: object, label: str) -> Path:
    if not isinstance(value, str):
        raise BundleError("invalid recovery bundle configuration")
    path = Path(value)
    if not path.is_absolute() or ".." in path.parts:
        raise BundleError(f"{label} must be absolute")
    return path


def _archive_name(value: object, label: str) -> str:
    if not isinstance(value, str) or not value:
        raise BundleError("invalid recovery bundle configuration")
    name = value.lstrip("/")
    path = PurePosixPath(name)
    if not name or path.is_absolute() or ".." in path.parts or "." in path.parts:
        raise BundleError(f"{label} must be a safe restore path")
    return str(path)


def load_config(path: Path) -> dict:
    try:
        with path.open(encoding="utf-8") as handle:
            config = json.load(handle)
    except (OSError, ValueError, TypeError) as exc:
        raise BundleError("cannot read recovery bundle configuration") from exc
    if not isinstance(config, dict):
        raise BundleError("invalid recovery bundle configuration")
    return config


def validate_private_path(path: Path, label: str, *, directory: bool = False) -> None:
    try:
        info = path.lstat()
    except OSError as exc:
        raise BundleError(f"invalid {label}") from exc
    if path.is_symlink() or info.st_uid != os.geteuid():
        raise BundleError(f"invalid {label}")
    if directory:
        if not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o700:
            raise BundleError(f"{label} must be an owner-only directory")
    elif not stat.S_ISREG(info.st_mode):
        raise BundleError(f"invalid {label}")


def validate_config(config: dict) -> tuple[list[dict], list[Path], list[Path], list[Path], Path | None, Path | None, list[str] | None]:
    databases = config.get("sqlite_databases", [])
    paths = config.get("paths", [])
    excluded = config.get("excluded_paths", [])
    required = config.get("required_files", [])
    if not isinstance(databases, list) or not isinstance(paths, list) or not isinstance(excluded, list) or not isinstance(required, list):
        raise BundleError("invalid recovery bundle configuration")
    db_specs = []
    for item in databases:
        if not isinstance(item, dict):
            raise BundleError("invalid recovery bundle configuration")
        source = _absolute(item.get("source"), "sqlite source")
        archive = _archive_name(item.get("archive_path"), "sqlite archive_path")
        try:
            info = source.lstat()
        except OSError as exc:
            raise BundleError("invalid SQLite source") from exc
        if source.is_symlink() or not stat.S_ISREG(info.st_mode):
            raise BundleError("invalid SQLite source")
        db_specs.append({"source": source, "archive": archive})
    path_list = [_absolute(item, "path") for item in paths]
    excluded_list = [_absolute(item, "excluded_path") for item in excluded]
    required_list = [_absolute(item, "required_file") for item in required]
    if len({item["archive"] for item in db_specs}) != len(db_specs):
        raise BundleError("duplicate SQLite archive path")
    for item in path_list + excluded_list + required_list:
        if item.is_symlink():
            raise BundleError("configuration paths may not be symlinks")
        if not item.exists():
            raise BundleError("configured source does not exist")
    for required_file in required_list:
        if any(required_file == excluded_item or required_file.is_relative_to(excluded_item) for excluded_item in excluded_list):
            raise BundleError("required file is excluded")
    for index, source in enumerate(path_list):
        for other in path_list[index + 1 :]:
            if source == other or source.is_relative_to(other) or other.is_relative_to(source):
                raise BundleError("overlapping configured paths")
    dump_dir = None
    if config.get("postgres_dump_directory") is not None:
        dump_dir = _absolute(config["postgres_dump_directory"], "postgres_dump_directory")
        if not dump_dir.is_dir():
            raise BundleError("invalid PostgreSQL dump directory")
    pg_restore = None
    if dump_dir is not None:
        pg_restore = _absolute(config.get("pg_restore_binary"), "pg_restore_binary")
        validate_private_path(pg_restore, "pg_restore binary")
        if not os.access(pg_restore, os.X_OK):
            raise BundleError("pg_restore binary is not executable")
    globals_command = config.get("postgres_globals_command")
    if globals_command is not None:
        if not isinstance(globals_command, list) or not globals_command or not all(isinstance(item, str) and item for item in globals_command):
            raise BundleError("invalid PostgreSQL globals command")
        binary = _absolute(globals_command[0], "postgres_globals_command binary")
        validate_private_path(binary, "PostgreSQL globals binary")
        if not os.access(binary, os.X_OK):
            raise BundleError("PostgreSQL globals binary is not executable")
        globals_command = [str(binary), *globals_command[1:]]
    return db_specs, path_list, excluded_list, required_list, dump_dir, pg_restore, globals_command


def excluded(path: Path, excluded_paths: list[Path]) -> bool:
    try:
        return any(path == item or path.is_relative_to(item) for item in excluded_paths)
    except OSError:
        return True


def digest(path: Path) -> str:
    hasher = hashlib.sha256()
    try:
        with path.open("rb") as handle:
            for block in iter(lambda: handle.read(1024 * 1024), b""):
                hasher.update(block)
    except OSError as exc:
        raise BundleError("cannot hash required file") from exc
    return hasher.hexdigest()


def required_hashes(files: list[Path]) -> dict[Path, str]:
    result = {}
    for path in files:
        try:
            info = path.lstat()
        except OSError as exc:
            raise BundleError("required file is unavailable") from exc
        if path.is_symlink() or not stat.S_ISREG(info.st_mode):
            raise BundleError("required file must be regular")
        result[path] = digest(path)
    return result


def sqlite_online_backup(source: Path, destination: Path) -> None:
    uri = f"file:{quote(str(source), safe='/')}?mode=ro"
    source_db = destination_db = None
    try:
        source_db = sqlite3.connect(uri, uri=True)
        destination_db = sqlite3.connect(destination)
        source_db.backup(destination_db)
        result = destination_db.execute("PRAGMA integrity_check").fetchone()
        if result != ("ok",):
            raise BundleError("SQLite snapshot integrity check failed")
    except (sqlite3.Error, OSError) as exc:
        if isinstance(exc, BundleError):
            raise
        raise BundleError("SQLite snapshot failed") from exc
    finally:
        if source_db is not None:
            source_db.close()
        if destination_db is not None:
            destination_db.close()


def select_postgres_dump(directory: Path) -> Path:
    now = time.time()
    candidates = []
    try:
        for item in directory.glob("money-manager-*.dump"):
            if item.name.startswith(".") or not item.is_file() or item.stat().st_size == 0:
                continue
            age = now - item.stat().st_mtime
            if 0 <= age <= MAX_DUMP_AGE_HOURS * 3600:
                candidates.append(item)
    except OSError as exc:
        raise BundleError("cannot inspect PostgreSQL dumps") from exc
    if not candidates:
        raise BundleError("no fresh PostgreSQL dump")
    return max(candidates, key=lambda item: item.stat().st_mtime)


def validate_pg_dump(dump: Path, pg_restore: Path) -> None:
    try:
        completed = subprocess.run(
            [str(pg_restore), "--list", str(dump)],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            check=False,
            timeout=120,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        raise BundleError("PostgreSQL dump validation failed") from exc
    if completed.returncode != 0:
        raise BundleError("PostgreSQL dump validation failed")


def copy_stable_dump(source: Path, run_dir: Path) -> Path:
    try:
        before_info = source.stat()
    except OSError as exc:
        raise BundleError("PostgreSQL dump changed during capture") from exc
    before_metadata = (before_info.st_ino, before_info.st_size, before_info.st_mtime_ns)
    before = digest(source)
    try:
        stable = run_dir / "postgres.dump"
        shutil.copyfile(source, stable)
        after = digest(source)
        copied = digest(stable)
        source_info = source.stat()
    except OSError as exc:
        raise BundleError("PostgreSQL dump changed during capture") from exc
    after_metadata = (source_info.st_ino, source_info.st_size, source_info.st_mtime_ns)
    if before != after or before != copied or before_metadata != after_metadata or source_info.st_size == 0:
        raise BundleError("PostgreSQL dump changed during capture")
    return stable


def capture_postgres_globals(command: list[str], run_dir: Path) -> Path:
    output = run_dir / "postgres-globals.sql"
    try:
        with output.open("wb") as handle:
            completed = subprocess.run(
                command,
                stdin=subprocess.DEVNULL,
                stdout=handle,
                stderr=subprocess.PIPE,
                check=False,
                timeout=120,
            )
    except (OSError, subprocess.SubprocessError) as exc:
        raise BundleError("PostgreSQL globals capture failed") from exc
    try:
        nonempty = output.stat().st_size > 0
    except OSError as exc:
        raise BundleError("PostgreSQL globals capture failed") from exc
    if completed.returncode != 0 or not nonempty:
        raise BundleError("PostgreSQL globals capture failed")
    return output


def tar_add_tree(archive: tarfile.TarFile, source: Path, excluded_paths: list[Path], sqlite_sources: set[Path]) -> None:
    arcroot = source.as_posix().lstrip("/")
    if excluded(source, excluded_paths) or any(source == db or source == Path(f"{db}-wal") or source == Path(f"{db}-shm") for db in sqlite_sources):
        return
    if source.is_dir() and not source.is_symlink():
        archive.add(source, arcname=arcroot, recursive=False)
        try:
            children = sorted(source.iterdir(), key=lambda item: item.name)
        except OSError as exc:
            raise BundleError("cannot read configured source") from exc
        for child in children:
            if not excluded(child, excluded_paths):
                tar_add_tree(archive, child, excluded_paths, sqlite_sources)
    else:
        archive.add(source, arcname=arcroot, recursive=False)


def create_tar(config: dict, db_specs: list[dict], paths: list[Path], excluded_paths: list[Path], required_files: list[Path], dump: Path | None, dump_archive_name: str | None, globals_file: Path | None, run_dir: Path) -> Path:
    artifact = run_dir / "recovery.tar.gz"
    snapshot_dir = run_dir / "sqlite"
    snapshot_dir.mkdir(mode=0o700)
    with tarfile.open(artifact, "w:gz", dereference=False) as archive:
        for index, spec in enumerate(db_specs):
            snapshot = snapshot_dir / f"{index}.db"
            sqlite_online_backup(spec["source"], snapshot)
            archive.add(snapshot, arcname=spec["archive"], recursive=False)
        sqlite_sources = {spec["source"] for spec in db_specs}
        for source in paths:
            tar_add_tree(archive, source, excluded_paths, sqlite_sources)
        for required in required_files:
            if not excluded(required, excluded_paths) and not any(required == path or required.is_relative_to(path) for path in paths):
                tar_add_tree(archive, required, excluded_paths, sqlite_sources)
        if dump is not None and not excluded(dump, excluded_paths):
            archive.add(dump, arcname=dump_archive_name or dump.as_posix().lstrip("/"), recursive=False)
        if globals_file is not None:
            archive.add(globals_file, arcname="postgres-globals.sql", recursive=False)
    return artifact


def restic_password_ok() -> None:
    if not os.environ.get("RESTIC_REPOSITORY") or not os.environ.get("RESTIC_PASSWORD_FILE"):
        raise BundleError("RESTIC_REPOSITORY and RESTIC_PASSWORD_FILE are required")
    if any(name in os.environ for name in ("RESTIC_PASSWORD", "RESTIC_PASSWORD_COMMAND", "RESTIC_REPOSITORY_FILE")):
        raise BundleError("RESTIC password alternatives are not allowed")
    password = Path(os.environ["RESTIC_PASSWORD_FILE"])
    validate_private_path(password, "restic password file")
    if password.stat().st_size == 0 or password.stat().st_mode & 0o077:
        raise BundleError("restic password file must be private and nonempty")


def restic_run(restic: Path, args: list[str], *, stdin=None, stdout=None, run_dir: Path) -> subprocess.CompletedProcess:
    try:
        return subprocess.run([str(restic), "--no-cache", *args], stdin=stdin, stdout=stdout, stderr=subprocess.PIPE, check=False, cwd=run_dir)
    except OSError as exc:
        raise BundleError("restic operation failed") from exc


def upload_and_verify(restic: Path, artifact: Path, environment: str, run_dir: Path) -> str:
    output = run_dir / "restic.jsonl"
    with artifact.open("rb") as input_file, output.open("wb") as output_file:
        result = restic_run(restic, ["backup", "--json", "--tag", environment, "--tag", "recovery", "--stdin", "--stdin-filename", "recovery.tar.gz"], stdin=input_file, stdout=output_file, run_dir=run_dir)
    if result.returncode != 0:
        raise BundleError("restic upload failed")
    snapshot_ids = []
    try:
        for line in output.read_text(encoding="utf-8").splitlines():
            item = json.loads(line)
            if isinstance(item, dict) and item.get("message_type") == "summary" and isinstance(item.get("snapshot_id"), str) and SNAPSHOT_ID_RE.fullmatch(item["snapshot_id"]):
                snapshot_ids.append(item["snapshot_id"])
    except (OSError, ValueError, UnicodeError) as exc:
        raise BundleError("restic upload response invalid") from exc
    if len(snapshot_ids) != 1:
        raise BundleError("restic upload response invalid")
    snapshot_id = snapshot_ids[0]
    restored = run_dir / "restic-recovered.tar.gz"
    with restored.open("wb") as output_file:
        result = restic_run(restic, ["dump", snapshot_id, "/recovery.tar.gz"], stdout=output_file, run_dir=run_dir)
    if result.returncode != 0:
        raise BundleError("restic verification failed")
    if not _same_bytes(artifact, restored):
        raise BundleError("restic verification failed")
    return snapshot_id


def _same_bytes(left: Path, right: Path) -> bool:
    try:
        if left.stat().st_size != right.stat().st_size:
            return False
        with left.open("rb") as a, right.open("rb") as b:
            while True:
                first, second = a.read(1024 * 1024), b.read(1024 * 1024)
                if first != second:
                    return False
                if not first:
                    return True
    except OSError:
        return False


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("--work-root", required=True, type=Path)
    parser.add_argument("--restic-binary", required=True, type=Path)
    parser.add_argument("--environment", required=True)
    args = parser.parse_args(argv)
    if not args.config.is_absolute() or not args.work_root.is_absolute() or not args.restic_binary.is_absolute():
        parser.error("--config, --work-root, and --restic-binary must be absolute")
    if not ENVIRONMENT_RE.fullmatch(args.environment):
        parser.error("invalid --environment")
    return args


def main(argv: list[str] | None = None) -> int:
    previous_handlers = {}
    try:
        os.umask(0o077)
        for signal_number in (signal.SIGINT, signal.SIGTERM):
            previous_handlers[signal_number] = signal.getsignal(signal_number)
            signal.signal(signal_number, handle_interrupt)
        args = parse_args(sys.argv[1:] if argv is None else argv)
        validate_private_path(args.config, "config file")
        if args.config.stat().st_mode & 0o077:
            raise BundleError("config file must be owner-only")
        validate_private_path(args.work_root, "work root", directory=True)
        validate_private_path(args.restic_binary, "restic binary")
        if not os.access(args.restic_binary, os.X_OK):
            raise BundleError("restic binary is not executable")
        restic_password_ok()
        config = load_config(args.config)
        db_specs, paths, excluded_paths, required_files, dump_dir, pg_restore, globals_command = validate_config(config)
        for source in paths:
            if args.work_root == source or args.work_root.is_relative_to(source):
                if not any(args.work_root == item or args.work_root.is_relative_to(item) for item in excluded_paths):
                    raise BundleError("work root is inside a captured path")
        protected = [Path(os.environ["RESTIC_PASSWORD_FILE"]), args.config.parent]
        for target in protected:
            captured = any(target == source or target.is_relative_to(source) for source in paths + required_files + [item["source"] for item in db_specs])
            if captured and not excluded(target, excluded_paths):
                raise BundleError("recovery sources include protected credentials")
        before = required_hashes(required_files)
        dump = None
        if dump_dir is not None:
            dump = select_postgres_dump(dump_dir)
        with tempfile.TemporaryDirectory(prefix="recovery-bundle.", dir=args.work_root) as temporary:
            run_dir = Path(temporary)
            stable_dump = None
            dump_archive_name = None
            if dump is not None:
                stable_dump = copy_stable_dump(dump, run_dir)
                validate_pg_dump(stable_dump, pg_restore)
                dump_archive_name = dump.as_posix().lstrip("/")
            globals_file = capture_postgres_globals(globals_command, run_dir) if globals_command is not None else None
            artifact = create_tar(config, db_specs, paths, excluded_paths, required_files, stable_dump, dump_archive_name, globals_file, run_dir)
            if required_hashes(required_files) != before:
                raise BundleError("required configuration changed during capture")
            snapshot_id = upload_and_verify(args.restic_binary, artifact, args.environment, run_dir)
        print(f"recovery bundle verified: environment={args.environment} snapshot={snapshot_id}")
        return 0
    except (BundleError, OSError, sqlite3.Error):
        print("recovery bundle failed", file=sys.stderr)
        return 1
    except (BundleInterrupted, KeyboardInterrupt):
        print("recovery bundle interrupted", file=sys.stderr)
        return 130
    finally:
        for signal_number, handler in previous_handlers.items():
            signal.signal(signal_number, handler)


if __name__ == "__main__":
    raise SystemExit(main())
