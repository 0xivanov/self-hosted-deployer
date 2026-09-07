#!/usr/bin/env python3
"""Serve one private platform-backup Prometheus exposition file."""

import argparse
import ipaddress
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import re
import socket
import sys
from urllib.parse import urlsplit


MAX_METRICS_BYTES = 64 * 1024
_SAMPLE = re.compile(
    r"^[a-zA-Z_:][a-zA-Z0-9_:]*(?:\{[^{}\n]*\})?\s+"
    r"(?:[-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?|NaN|[+-]?Inf)"
    r"(?:\s+\d+)?$"
)
_HELP = re.compile(r"^# HELP [a-zA-Z_:][a-zA-Z0-9_:]* .+$")
_TYPE = re.compile(r"^# TYPE [a-zA-Z_:][a-zA-Z0-9_:]* (counter|gauge|histogram|gaugehistogram|summary|info|stateset|unknown)$")


def read_metrics(path: Path) -> bytes | None:
    """Read and minimally validate a bounded Prometheus text exposition."""
    try:
        with path.open("rb") as metrics_file:
            body = metrics_file.read(MAX_METRICS_BYTES + 1)
    except (OSError, ValueError):
        return None
    if len(body) > MAX_METRICS_BYTES:
        return None
    try:
        text = body.decode("utf-8")
    except UnicodeDecodeError:
        return None
    if not text or "\x00" in text:
        return None
    samples = 0
    for line in text.splitlines():
        if not line:
            continue
        if line.startswith("#"):
            if not (_HELP.fullmatch(line) or _TYPE.fullmatch(line) or line.startswith("# EOF")):
                return None
            continue
        if not _SAMPLE.fullmatch(line):
            return None
        samples += 1
    return body if samples else None


class MetricsHandler(BaseHTTPRequestHandler):
    server_version = "deployer-backup-metrics/1"

    def log_message(self, _format: str, *_args: object) -> None:
        # The metrics endpoint must never echo metric contents into logs.
        return

    def do_GET(self) -> None:  # noqa: N802 - required by BaseHTTPRequestHandler
        if urlsplit(self.path).path != "/metrics":
            self.send_error(HTTPStatus.NOT_FOUND)
            return
        body = read_metrics(self.server.metrics_file)  # type: ignore[attr-defined]
        if body is None:
            self.send_error(HTTPStatus.SERVICE_UNAVAILABLE)
            return
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class MetricsServer(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = False

    def __init__(self, address: tuple[str, int], metrics_file: Path):
        super().__init__(address, MetricsHandler)
        self.metrics_file = metrics_file


class MetricsServerV6(MetricsServer):
    address_family = socket.AF_INET6


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--metrics-file", required=True, type=Path)
    parser.add_argument("--listen", required=True, help="Explicit address to bind")
    parser.add_argument("--port", required=True, type=int)
    args = parser.parse_args()
    if not args.metrics_file.is_absolute():
        parser.error("--metrics-file must be an absolute path")
    if not 1 <= args.port <= 65535:
        parser.error("--port must be between 1 and 65535")
    try:
        ipaddress.ip_address(args.listen)
    except ValueError:
        parser.error("--listen must be an explicit IPv4 or IPv6 address")
    return args


def main() -> int:
    args = parse_args()
    try:
        address = (args.listen, args.port, 0, 0) if ":" in args.listen else (args.listen, args.port)
        server_type = MetricsServerV6 if ":" in args.listen else MetricsServer
        server = server_type(address, args.metrics_file)
    except OSError as error:
        print(f"could not bind metrics listener: {error}", file=sys.stderr)
        return 1
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
