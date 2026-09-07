#!/usr/bin/env python3
"""Run the platform backup command once and publish local Prometheus metrics."""

import argparse
import fcntl
import os
from pathlib import Path
import subprocess
import sys
import signal
import stat
import tempfile
import time


METRICS_NAME = "platform-backup.prom"
LOCK_NAME = ".platform-backup.lock"


def parse_args(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-directory", required=True, type=Path)
    try:
        separator = argv.index("--")
    except ValueError:
        if "--help" in argv or "-h" in argv:
            parser.parse_args(argv)
        parser.error("-- is required before the backup command")
    options = parser.parse_args(argv[:separator])
    command = argv[separator + 1 :]
    if not command:
        parser.error("a backup command is required after --")
    if not options.state_directory.is_absolute():
        parser.error("--state-directory must be absolute")
    return options.state_directory, command


def ensure_private_state_directory(path):
    try:
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
        info = path.lstat()
    except OSError as exc:
        raise RuntimeError("cannot prepare private state directory") from exc
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise RuntimeError("state directory must be an owner-only directory")


def read_previous_success(metrics_path):
    try:
        for line in metrics_path.read_text(encoding="ascii").splitlines():
            if line.startswith("deployer_platform_backup_last_success_timestamp_seconds "):
                return line.split(" ", 1)[1]
    except (OSError, UnicodeError, IndexError):
        pass
    return "0"


def write_metrics(path, attempt, duration, success, previous_success, success_timestamp=None):
    lines = [
        "# TYPE deployer_platform_backup_last_success_timestamp_seconds gauge",
    ]
    last_success = str(success_timestamp if success_timestamp is not None else attempt) if success else (previous_success or "0")
    lines.append(f"deployer_platform_backup_last_success_timestamp_seconds {last_success}")
    lines.extend(
        [
            "# TYPE deployer_platform_backup_last_run_success gauge",
            f"deployer_platform_backup_last_run_success {1 if success else 0}",
            "# TYPE deployer_platform_backup_last_attempt_timestamp_seconds gauge",
            f"deployer_platform_backup_last_attempt_timestamp_seconds {attempt}",
            "# TYPE deployer_platform_backup_duration_seconds gauge",
            f"deployer_platform_backup_duration_seconds {duration:.6f}",
            "",
        ]
    )
    payload = "\n".join(lines).encode("ascii")
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="wb", dir=path.parent, prefix=f".{path.name}.", delete=False
        ) as handle:
            temporary = Path(handle.name)
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, 0o644)
        os.replace(temporary, path)
    finally:
        if temporary is not None and temporary.exists():
            temporary.unlink()


def run(state_directory, command):
    ensure_private_state_directory(state_directory)
    lock_path = state_directory / LOCK_NAME
    metrics_path = state_directory / METRICS_NAME
    lock_file = lock_path.open("a+")
    os.chmod(lock_path, 0o600)
    try:
        try:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print("platform backup already running; skipping", file=sys.stderr)
            return 0

        previous_success = read_previous_success(metrics_path)
        attempt = int(time.time())
        started = time.monotonic()
        success = False
        exit_code = 1
        interrupted = None
        success_timestamp = None
        previous_handlers = {}
        for signal_number in (signal.SIGINT, signal.SIGTERM):
            previous_handlers[signal_number] = signal.getsignal(signal_number)
            signal.signal(signal_number, signal.default_int_handler)
        try:
            process = subprocess.Popen(command, start_new_session=True)
            try:
                return_code = process.wait()
            except KeyboardInterrupt:
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    process.wait()
                raise
            success = return_code == 0
            if success:
                success_timestamp = int(time.time())
            exit_code = 0 if success else 1
            if not success:
                print("platform backup command failed", file=sys.stderr)
        except KeyboardInterrupt as exc:
            interrupted = exc
            print("platform backup interrupted", file=sys.stderr)
        except OSError:
            print("platform backup command could not be started", file=sys.stderr)
        finally:
            duration = max(0.0, time.monotonic() - started)
            write_metrics(metrics_path, attempt, duration, success, previous_success, success_timestamp)
            for signal_number, handler in previous_handlers.items():
                signal.signal(signal_number, handler)
        if interrupted is not None:
            raise interrupted
        return exit_code
    finally:
        fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)
        lock_file.close()


def main(argv=None):
    try:
        state_directory, command = parse_args(sys.argv[1:] if argv is None else argv)
        return run(state_directory, command)
    except KeyboardInterrupt:
        return 130
    except (OSError, RuntimeError):
        print("platform backup wrapper failed", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
