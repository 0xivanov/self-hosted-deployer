#!/usr/bin/env python3
"""Stage a reviewed recovery archive in a new private directory.

This is an offline staging helper.  It deliberately does not restore an
archive to system paths or preserve archive ownership and special files.
"""

from __future__ import annotations

import argparse
import hashlib
import os
from pathlib import Path, PurePosixPath
import shutil
import stat
import sys
import tarfile
import tempfile


class StageError(Exception):
    """A safe, user-facing staging failure."""


def validate_archive_path(path: Path, label: str) -> None:
    try:
        info = path.lstat()
    except OSError as exc:
        raise StageError(f"{label} is unavailable") from exc
    if path.is_symlink() or not stat.S_ISREG(info.st_mode):
        raise StageError(f"{label} must be a regular file")


def validate_output_parent(parent: Path) -> None:
    if not parent.is_absolute():
        raise StageError("output parent must be absolute")
    current = parent
    first = True
    while True:
        try:
            info = current.lstat()
        except OSError as exc:
            raise StageError("output parent is unavailable") from exc
        if current.is_symlink() or not stat.S_ISDIR(info.st_mode):
            raise StageError("output parent must contain no symlink components and be a directory")
        if first and (info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o077):
            raise StageError("output parent must be a private directory owned by the current user")
        if current == current.parent:
            break
        current = current.parent
        first = False


def member_name(member: tarfile.TarInfo) -> str:
    raw = member.name
    if not isinstance(raw, str) or not raw or "\x00" in raw:
        raise StageError("archive contains an unsafe member path")
    if raw.startswith("/"):
        raise StageError("archive contains an absolute member path")
    parts = raw.rstrip("/").split("/")
    if not parts or any(part in ("", ".", "..") for part in parts):
        raise StageError("archive contains an unsafe member path")
    return "/".join(parts)


def validate_members(archive: tarfile.TarFile) -> list[tuple[tarfile.TarInfo, str]]:
    entries: list[tuple[tarfile.TarInfo, str]] = []
    names: set[str] = set()
    kinds: dict[str, bool] = {}
    try:
        members = archive.getmembers()
    except (tarfile.TarError, OSError) as exc:
        raise StageError("cannot inspect archive") from exc
    for member in members:
        name = member_name(member)
        if not (member.isdir() or member.isreg()):
            raise StageError("archive contains a link, device, FIFO, or other special member")
        is_dir = member.isdir()
        if name in names:
            raise StageError("archive contains duplicate member paths")
        names.add(name)
        if name in kinds and kinds[name] != is_dir:
            raise StageError("archive contains conflicting member paths")
        kinds[name] = is_dir
        entries.append((member, name))
    for member, name in entries:
        prefix = PurePosixPath(name)
        for parent in prefix.parents:
            if str(parent) == ".":
                continue
            parent_name = str(parent)
            if parent_name in kinds and not kinds[parent_name]:
                raise StageError("archive contains a file used as a directory")
    return entries


def safe_mode(member: tarfile.TarInfo) -> int:
    # Ownership and setuid/setgid are intentionally never restored.
    return member.mode & 0o777


def ensure_directory(root: Path, relative: PurePosixPath) -> None:
    current = root
    for part in relative.parts:
        current /= part
        if current.exists() or current.is_symlink():
            try:
                info = current.lstat()
            except OSError as exc:
                raise StageError("cannot inspect staged path") from exc
            if current.is_symlink() or not stat.S_ISDIR(info.st_mode):
                raise StageError("archive path is not a directory")
        else:
            current.mkdir(mode=0o700)


def extract_entries(archive: tarfile.TarFile, entries: list[tuple[tarfile.TarInfo, str]], root: Path) -> None:
    for member, name in entries:
        relative = PurePosixPath(name)
        target = root.joinpath(*relative.parts)
        if member.isdir():
            ensure_directory(root, relative)
            continue
        ensure_directory(root, relative.parent)
        if target.exists() or target.is_symlink():
            raise StageError("archive path already exists during extraction")
        try:
            fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, safe_mode(member))
            with os.fdopen(fd, "wb") as output, archive.extractfile(member) as source:
                if source is None:
                    raise StageError("cannot read regular archive member")
                shutil.copyfileobj(source, output)
            target.chmod(safe_mode(member))
        except (OSError, tarfile.TarError) as exc:
            raise StageError("cannot extract archive member") from exc


def stage(archive_path: Path, expected: str, output: Path) -> None:
    validate_archive_path(archive_path, "archive")
    if len(expected) != 64 or any(character not in "0123456789abcdefABCDEF" for character in expected):
        raise StageError("sha256 must be 64 hexadecimal characters")
    if ".." in output.parts:
        raise StageError("output must not contain parent traversal")
    validate_output_parent(output.parent)
    if output.exists() or output.is_symlink():
        raise StageError("output directory already exists")

    created = False
    try:
        # Hash exactly the private snapshot later parsed, even if the source
        # pathname or source bytes change during staging.
        with tempfile.TemporaryFile(dir=output.parent) as snapshot:
            hasher = hashlib.sha256()
            with archive_path.open("rb") as source:
                for block in iter(lambda: source.read(1024 * 1024), b""):
                    snapshot.write(block)
                    hasher.update(block)
            if hasher.hexdigest() != expected.lower():
                raise StageError("archive checksum does not match")
            snapshot.seek(0)
            with tarfile.open(fileobj=snapshot, mode="r:*") as archive:
                entries = validate_members(archive)
                # mkdir is an exclusive reservation: never rename over an
                # existing directory, including another concurrent attempt.
                output.mkdir(mode=0o700)
                created = True
                extract_entries(archive, entries, output)
                # Apply directory modes last so archive ordering cannot strip
                # traversal permission while children are still being written.
                for member, name in sorted(entries, key=lambda item: item[1].count("/"), reverse=True):
                    if member.isdir():
                        output.joinpath(*PurePosixPath(name).parts).chmod(safe_mode(member))
        output.chmod(0o700)
        created = False
    except (OSError, tarfile.TarError) as exc:
        raise StageError("cannot stage recovery archive") from exc
    finally:
        if created:
            # Directory modes may already have been tightened on failure.
            for parent, dirs, _ in os.walk(output, topdown=True):
                os.chmod(parent, 0o700)
                for name in dirs:
                    os.chmod(Path(parent) / name, 0o700)
            shutil.rmtree(output)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--sha256", required=True, help="expected archive SHA-256")
    parser.add_argument("--output", required=True, type=Path, help="new staging directory")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    try:
        args = parse_args(sys.argv[1:] if argv is None else argv)
        stage(args.archive, args.sha256, args.output)
        print(f"recovery bundle staged: {args.output}")
        return 0
    except StageError as exc:
        print(f"recovery bundle staging failed: {exc}", file=sys.stderr)
        return 1
    except OSError:
        print("recovery bundle staging failed: filesystem error", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
