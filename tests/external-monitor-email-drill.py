#!/usr/bin/env python3
"""Explicitly authorized live email drill using a private local TLS fixture."""
import copy
import hashlib
import http.server
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tempfile
import threading


def main():
    if os.geteuid() != 0 or socket.gethostname() != "vanko1" or sys.argv[1:] != ["--send-test-emails"]:
        raise SystemExit("Requires root on the home Pi and explicit --send-test-emails authorization")
    os.umask(0o077)
    source = Path('/etc/deployer-external-monitor/config.json')
    original = source.read_bytes()
    config = json.loads(original)
    assert config['smtp']['to'] == ['ivanivanov.ii726@gmail.com']
    assert config['smtp']['host'] == 'smtp.gmail.com'
    parent = Path('/var/lib/deployer-external-monitor-tests')
    parent.mkdir(mode=0o700, exist_ok=True)
    assert not parent.is_symlink() and parent.stat().st_uid == 0 and parent.stat().st_mode & 0o077 == 0
    with tempfile.TemporaryDirectory(dir=parent) as temporary:
        work = Path(temporary)
        cert, key = work/'cert.pem', work/'key.pem'
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1',
                        '-subj','/CN=localhost','-addext','subjectAltName=IP:127.0.0.1,DNS:localhost',
                        '-keyout',str(key),'-out',str(cert)], check=True, capture_output=True)
        healthy = False

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200 if healthy else 503)
                self.end_headers()
                self.wfile.write(b'synthetic monitor drill\n')

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(cert, key)
        server.socket = tls.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            # Trust the fixture only in the child monitor processes. Keep the
            # system roots so SMTP still uses normal verified provider TLS.
            bundle = work/'ca-bundle.pem'
            bundle.write_bytes(Path('/etc/ssl/certs/ca-certificates.crt').read_bytes()+b'\n'+cert.read_bytes())
            drill = copy.deepcopy(config)
            drill['environment'] = 'TEST ONLY - legacy-vps monitor qualification'
            drill['failure_threshold'] = 1
            drill['checks'] = [{'name':'synthetic-outage-recovery','url':f'https://127.0.0.1:{server.server_port}/',
                                'expected_status':200,'minimum_certificate_days':0}]
            config_path = work/'config.json'
            config_path.write_text(json.dumps(drill))
            state = work/'state'
            command = ['python3','/usr/local/libexec/external-uptime-monitor.py','--config',str(config_path),'--state-directory',str(state)]
            for label, expected in [('outage', True), ('recovery', False)]:
                subprocess.run(command, env={**os.environ,'SSL_CERT_FILE':str(bundle)}, check=True, timeout=90)
                observed = json.loads((state/'state.json').read_text())['checks']['synthetic-outage-recovery']
                assert observed['notified'] is expected and observed['last_sent'] > 0
                print(f'PASS: labeled {label} email accepted by SMTP', flush=True)
                healthy = True
            assert hashlib.sha256(source.read_bytes()).digest() == hashlib.sha256(original).digest()
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)


if __name__ == '__main__':
    main()
