#!/usr/bin/env python3
"""Copy retained mutation audit records from journald with durable cursor tracking."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile


class CollectorError(Exception):
    pass


def private_dir(path):
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise CollectorError('directory must be private and owned')


def private_file(path):
    try:
        info = path.lstat()
    except FileNotFoundError:
        return
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise CollectorError('file must be private and owned')


def persist_cursor(path, value):
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, delete=False) as output:
            temporary = Path(output.name)
            output.write(value + '\n')
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        with open_directory(path.parent) as descriptor:
            os.fsync(descriptor)
    finally:
        if temporary and temporary.exists():
            temporary.unlink()


class open_directory:
    def __init__(self, path):
        self.path = path

    def __enter__(self):
        self.fd = os.open(self.path, os.O_RDONLY | os.O_DIRECTORY)
        return self.fd

    def __exit__(self, *_):
        os.close(self.fd)


def collect(output_path, cursor_path, unit):
    cursor = cursor_path.read_text(encoding='ascii').strip() if cursor_path.exists() else ''
    command = ['journalctl', '--no-pager', '-o', 'json', '-u', unit]
    # Inclusive lookup proves the saved cursor still exists. journalctl may
    # otherwise seek past a vacuumed cursor without returning an error.
    if cursor:
        command += ['--cursor', cursor]
    count, latest, matched = 0, cursor, not bool(cursor)
    with tempfile.TemporaryFile() as errors, output_path.open('a', encoding='utf-8') as output:
        process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=errors)
        try:
            while True:
                raw = process.stdout.readline(1024 * 1024 + 1)
                if not raw:
                    break
                if len(raw) > 1024 * 1024 or not raw.endswith(b'\n'):
                    raise CollectorError('invalid journal record')
                outer = json.loads(raw)
                if not isinstance(outer, dict) or not isinstance(outer.get('__CURSOR'), str):
                    raise CollectorError('invalid journal cursor')
                if not matched:
                    if outer['__CURSOR'] != cursor:
                        raise CollectorError('saved journal cursor is unavailable')
                    matched = True
                    continue
                latest = outer['__CURSOR']
                message = outer.get('MESSAGE')
                if not isinstance(message, str):
                    continue
                try:
                    inner = json.loads(message)
                except ValueError:
                    continue
                if isinstance(inner, dict) and inner.get('msg') == 'mutation audit':
                    output.write(json.dumps(inner, sort_keys=True, separators=(',', ':')) + '\n')
                    count += 1
            returncode = process.wait()
            output.flush()
            os.fsync(output.fileno())
            if returncode or errors.tell() or not matched:
                raise CollectorError('journal query failed or history is unavailable')
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()
            process.stdout.close()
    if latest and latest != cursor:
        persist_cursor(cursor_path, latest)
    return count


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--cursor', required=True, type=Path)
    parser.add_argument('--unit', default='deployer-server.service')
    args = parser.parse_args(argv)
    os.umask(0o077)
    try:
        if not args.output.is_absolute() or not args.cursor.is_absolute() or args.output == args.cursor:
            raise CollectorError('invalid collector paths')
        args.output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        private_dir(args.output.parent)
        private_dir(args.cursor.parent)
        lock_path = args.cursor.with_suffix(args.cursor.suffix + '.lock')
        private_file(lock_path)
        with lock_path.open('a') as lock:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
            private_file(args.output)
            private_file(args.cursor)
            args.output.touch(mode=0o600, exist_ok=True)
            count = collect(args.output, args.cursor, args.unit)
        print(f'audit journal collected: records={count}')
        return 0
    except (CollectorError, OSError, ValueError, UnicodeError):
        print('audit journal collection failed; check protected state and retained journal history', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
