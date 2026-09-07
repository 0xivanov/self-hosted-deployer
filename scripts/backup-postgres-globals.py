#!/usr/bin/env python3
"""Capture PostgreSQL globals as a private, atomically replaced SQL file."""

import argparse
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile


def fail(message):
    print(message, file=sys.stderr)
    return 1


def parse_args(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pg-dumpall", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args(argv)
    if not args.pg_dumpall.is_absolute() or not args.output.is_absolute():
        parser.error("--pg-dumpall and --output must be absolute")
    return args


def validate(args):
    try:
        binary = args.pg_dumpall.lstat()
        parent = args.output.parent.lstat()
    except OSError as exc:
        raise RuntimeError("invalid globals capture path") from exc
    if not stat.S_ISREG(binary.st_mode) or not os.access(args.pg_dumpall, os.X_OK):
        raise RuntimeError("invalid pg_dumpall binary")
    if (
        not stat.S_ISDIR(parent.st_mode)
        or parent.st_uid != os.geteuid()
        or stat.S_IMODE(parent.st_mode) != 0o700
        or args.output.is_symlink()
    ):
        raise RuntimeError("output parent must be an owner-only directory")


def capture(args):
    validate(args)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="wb", dir=args.output.parent, prefix=f".{args.output.name}.", delete=False
        ) as handle:
            temporary = Path(handle.name)
            completed = subprocess.run(
                [str(args.pg_dumpall), "--username=postgres", "--globals-only"],
                stdin=subprocess.DEVNULL,
                stdout=handle,
                stderr=subprocess.DEVNULL,
                check=False,
                timeout=120,
            )
            handle.flush()
            os.fsync(handle.fileno())
        if completed.returncode != 0 or temporary.stat().st_size == 0:
            raise RuntimeError("PostgreSQL globals capture failed")
        os.chmod(temporary, 0o600)
        os.replace(temporary, args.output)
        temporary = None
    except (OSError, subprocess.SubprocessError) as exc:
        raise RuntimeError("PostgreSQL globals capture failed") from exc
    finally:
        if temporary is not None:
            try:
                temporary.unlink()
            except FileNotFoundError:
                pass


def main(argv=None):
    try:
        capture(parse_args(sys.argv[1:] if argv is None else argv))
        return 0
    except (RuntimeError, OSError):
        return fail("PostgreSQL globals capture failed")


if __name__ == "__main__":
    raise SystemExit(main())
