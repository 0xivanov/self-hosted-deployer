#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import tempfile
import ssl
import subprocess
import threading
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
import unittest
from unittest.mock import patch, MagicMock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('monitor', ROOT / 'scripts/external-uptime-monitor.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class MonitorTests(unittest.TestCase):
    def setUp(self):
        self.config = {'environment': 'pilot-a', 'checks': [{'name': 'api', 'url': 'https://example.test/readyz'}], 'failure_threshold': 3, 'repeat_seconds': 120}
        self.messages = []

    def notify(self, config, subject, body):
        self.messages.append((subject, body))

    def step(self, state, error, now, notify=None):
        return m.update(self.config, state, {'api': error}, now, notify or self.notify)

    def test_failure_threshold_repeat_and_single_recovery(self):
        state = {}
        for now in [1, 2]:
            state, errors = self.step(state, 'HTTPS failed', now)
            self.assertFalse(errors)
            self.assertFalse(self.messages)
        state, _ = self.step(state, 'HTTPS failed', 3)
        self.assertEqual(len(self.messages), 1)
        state, _ = self.step(state, 'HTTPS failed', 122)
        self.assertEqual(len(self.messages), 1)
        state, _ = self.step(state, 'HTTPS failed', 123)
        self.assertEqual(len(self.messages), 2)
        state, _ = self.step(state, None, 124)
        self.assertIn('RECOVERED', self.messages[-1][0])
        state, _ = self.step(state, None, 125)
        self.assertEqual(len(self.messages), 3)

    def test_failed_delivery_retries_alert_and_recovery(self):
        def broken(*_):
            raise RuntimeError('sensitive provider failure')
        state = {}
        for now in [1, 2, 3]:
            state, errors = self.step(state, 'down', now, broken)
        self.assertEqual(errors, ['api'])
        self.assertFalse(state['checks']['api'].get('notified'))
        state, _ = self.step(state, 'down', 4)
        self.assertEqual(len(self.messages), 1)
        state, errors = self.step(state, None, 5, broken)
        self.assertTrue(state['checks']['api']['notified'])
        state, errors = self.step(state, None, 6)
        self.assertFalse(state['checks']['api']['notified'])
        self.assertEqual(len(self.messages), 2)

    def test_endpoint_change_resets_failure_history(self):
        state = {}
        for now in [1, 2]:
            state, _ = self.step(state, 'down', now)
        self.config['checks'][0]['url'] = 'https://other.test/readyz'
        state, _ = self.step(state, 'down', 3)
        self.assertFalse(self.messages)
        self.assertEqual(state['checks']['api']['consecutive_failures'], 1)

    def test_private_config_and_https_are_required(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp) / 'config.json'
            p.write_text(json.dumps(self.config))
            p.chmod(0o644)
            with self.assertRaises(ValueError):
                m.load_config(p)
            p.chmod(0o600)
            self.assertEqual(m.load_config(p)['environment'], 'pilot-a')
            for url in ['http://example.test', 'https://user:password@example.test', 'file:///tmp/secret']:
                self.config['checks'][0]['url'] = url
                p.write_text(json.dumps(self.config))
                with self.assertRaises(ValueError):
                    m.load_config(p)

    def test_state_write_is_private_and_atomic(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp) / 'state.json'
            m.save_state(p, {'version': 1})
            self.assertEqual(json.loads(p.read_text()), {'version': 1})
            self.assertEqual(p.stat().st_mode & 0o777, 0o600)
            self.assertEqual(len(list(Path(tmp).iterdir())), 1)

    def test_verified_https_status_expiry_and_untrusted_certificate(self):
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(503 if self.path == '/down' else 200)
                self.end_headers()

            def log_message(self, *_):
                pass

        class TestServer(HTTPServer):
            def handle_error(self, request, address):
                if not isinstance(sys.exc_info()[1], ConnectionResetError):
                    super().handle_error(request, address)

        with tempfile.TemporaryDirectory() as tmp:
            cert, key = Path(tmp) / 'cert.pem', Path(tmp) / 'key.pem'
            subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                            '-keyout', str(key), '-out', str(cert), '-days', '2',
                            '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost'],
                           check=True, capture_output=True)
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(cert, key)
            server = TestServer(('127.0.0.1', 0), Handler)
            server.socket = context.wrap_socket(server.socket, server_side=True)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            check = {'url': f'https://localhost:{server.server_port}/ready', 'minimum_certificate_days': 0}
            try:
                self.assertEqual(m.probe(check), 'HTTPS or TLS check failed')
                trusted = ssl.create_default_context(cafile=str(cert))
                with patch.object(m.ssl, 'create_default_context', return_value=trusted):
                    self.assertIsNone(m.probe(check))
                    self.assertEqual(m.probe(dict(check, minimum_certificate_days=14)), 'TLS certificate approaching expiry')
                    self.assertEqual(m.probe(dict(check, url=check['url'].replace('/ready', '/down'))), 'unexpected HTTP status')
            finally:
                server.shutdown()
                server.server_close()
                thread.join()

    def test_partial_smtp_refusal_is_not_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            password = Path(tmp) / 'password'
            password.write_text('test-password')
            password.chmod(0o600)
            config = {'smtp': {'password_file': str(password), 'to': ['one@example.test', 'two@example.test'],
                               'from': 'monitor@example.test', 'username': 'test-user', 'host': 'smtp.example.test'}}
            client = MagicMock()
            client.send_message.return_value = {'two@example.test': (450, b'try later')}
            with patch.object(m.smtplib, 'SMTP_SSL', return_value=client):
                with self.assertRaises(RuntimeError):
                    m.send_email(config, 'Test', 'Test')
            client.login.assert_called_once_with('test-user', 'test-password')

    def test_network_failure_does_not_leak_url(self):
        with patch.object(m.http.client, 'HTTPSConnection', side_effect=OSError('secret URL')):
            self.assertEqual(m.probe(self.config['checks'][0]), 'HTTPS or TLS check failed')

    def test_lock_contention_is_not_reported_as_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            config = directory / 'config.json'
            config.write_text(json.dumps(self.config))
            config.chmod(0o600)
            lock_path = directory / 'monitor.lock'
            lock = lock_path.open('a')
            try:
                import fcntl
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                with patch.object(m, 'probe', return_value=None):
                    with patch.object(m, 'load_config', return_value=self.config):
                        with patch.object(m.argparse.ArgumentParser, 'parse_args', return_value=type('Args', (), {'config': config, 'state_directory': directory, 'check_only': False})()):
                            self.assertEqual(m.main(), 1)
            finally:
                lock.close()

    def test_normal_run_rejects_invalid_smtp_before_probe(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            config = directory / 'config.json'
            config.write_text(json.dumps(self.config))
            config.chmod(0o600)
            with patch.object(m, 'probe', side_effect=AssertionError('probe must not run')):
                with patch.object(m, 'load_config', return_value=self.config):
                    with patch.object(m.argparse.ArgumentParser, 'parse_args', return_value=type('Args', (), {
                            'config': config, 'state_directory': directory, 'check_only': False})()):
                        self.assertEqual(m.main(), 1)

    def test_check_only_does_not_require_smtp(self):
        with patch.object(m, 'probe', return_value=None):
            with patch.object(m, 'load_config', return_value=self.config):
                with patch.object(m.argparse.ArgumentParser, 'parse_args', return_value=type('Args', (), {
                        'config': Path('/unused'), 'state_directory': None, 'check_only': True})()):
                    with patch('builtins.print'):
                        self.assertEqual(m.main(), 0)


if __name__ == '__main__':
    unittest.main()
