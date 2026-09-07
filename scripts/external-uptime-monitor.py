#!/usr/bin/env python3
"""Check public HTTPS and certificate expiry from a host outside the target cluster."""
import argparse
from email.message import EmailMessage
import fcntl
import http.client
import json
import os
from pathlib import Path
import smtplib
import ssl
import stat
import tempfile
import time
import urllib.parse


def private_file(path):
    path = Path(path)
    info = path.lstat()
    if not path.is_absolute() or not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise ValueError('configuration and credential files must be private and owned')
    return path


def load_config(path):
    config = json.loads(private_file(path).read_text())
    if not isinstance(config.get('environment'), str) or not config['environment'].strip():
        raise ValueError('environment is required')
    checks = config.get('checks', [])
    if not checks or len(checks) > 8 or len({x['name'] for x in checks}) != len(checks):
        raise ValueError('provide uniquely named HTTPS checks')
    for check in checks:
        url = urllib.parse.urlsplit(check['url'])
        if url.scheme != 'https' or not url.hostname or url.username or url.password or url.fragment:
            raise ValueError('checks require HTTPS URLs without embedded credentials')
        if not isinstance(check.get('name'), str) or not check['name'].strip():
            raise ValueError('check name is required')
        if not 200 <= check.get('expected_status', 200) <= 299:
            raise ValueError('expected status must be a successful HTTP status')
        if not 0 <= check.get('minimum_certificate_days', 14) <= 365:
            raise ValueError('invalid certificate threshold')
    if not 1 <= config.get('failure_threshold', 3) <= 60 or config.get('repeat_seconds', 14400) < 60:
        raise ValueError('invalid notification thresholds')
    return config


def validate_smtp_config(config):
    settings = config.get('smtp')
    if not isinstance(settings, dict):
        raise ValueError('smtp configuration is required for normal runs')
    tls_mode = settings.get('tls_mode', 'ssl')
    if tls_mode not in ('ssl', 'starttls'):
        raise ValueError('SMTP requires verified TLS')
    port = settings.get('port', 465 if tls_mode == 'ssl' else 587)
    if isinstance(port, bool) or not isinstance(port, int) or not 1 <= port <= 65535:
        raise ValueError('SMTP port is invalid')
    for field in ('host', 'username', 'from'):
        value = settings.get(field)
        if not isinstance(value, str) or not value.strip() or '\r' in value or '\n' in value:
            raise ValueError(f'SMTP {field} is invalid')
    recipients = settings.get('to')
    if not isinstance(recipients, list) or not recipients or any(
            not isinstance(value, str) or not value.strip() or '\r' in value or '\n' in value
            for value in recipients):
        raise ValueError('SMTP recipients are invalid')
    password_path = settings.get('password_file')
    if not isinstance(password_path, str) or not password_path.strip():
        raise ValueError('SMTP password file is required')
    password = private_file(password_path).read_text().strip()
    if not password:
        raise ValueError('SMTP password file is empty')


def probe(check, timeout=10):
    url = urllib.parse.urlsplit(check['url'])
    connection = None
    try:
        connection = http.client.HTTPSConnection(url.hostname, url.port or 443, timeout=timeout,
                                                 context=ssl.create_default_context())
        connection.connect()
        expires = ssl.cert_time_to_seconds(connection.sock.getpeercert()['notAfter'])
        target = urllib.parse.urlunsplit(('', '', url.path or '/', url.query, ''))
        connection.request('GET', target, headers={'User-Agent': 'deployer-external-monitor/1'})
        response = connection.getresponse()
        if response.status != check.get('expected_status', 200):
            return 'unexpected HTTP status'
        if expires - time.time() < check.get('minimum_certificate_days', 14) * 86400:
            return 'TLS certificate approaching expiry'
        return None
    except (OSError, ValueError, KeyError, http.client.HTTPException):
        return 'HTTPS or TLS check failed'
    finally:
        if connection is not None:
            connection.close()


def send_email(config, subject, body):
    settings = config['smtp']
    if settings.get('tls_mode', 'ssl') not in ('ssl', 'starttls'):
        raise ValueError('SMTP requires verified TLS')
    password = private_file(settings['password_file']).read_text().strip()
    if not password or not settings.get('to'):
        raise ValueError('SMTP credentials and recipient required')
    message = EmailMessage()
    message['Subject'] = subject
    message['From'] = settings['from']
    message['To'] = ', '.join(settings['to'])
    message.set_content(body)
    context = ssl.create_default_context()
    if settings.get('tls_mode', 'ssl') == 'ssl':
        client = smtplib.SMTP_SSL(settings['host'], settings.get('port', 465), timeout=15, context=context)
    else:
        client = smtplib.SMTP(settings['host'], settings.get('port', 587), timeout=15)
    with client:
        if settings.get('tls_mode', 'ssl') == 'starttls':
            client.ehlo()
            client.starttls(context=context)
            client.ehlo()
        client.login(settings['username'], password)
        if client.send_message(message):
            raise RuntimeError('SMTP rejected one or more recipients')


def update(config, state, results, now, notify=send_email):
    """Preserve undelivered transitions so a later execution retries them."""
    failures = []
    next_state = {'version': 1, 'checks': {}}
    for check in config['checks']:
        previous = state.get('checks', {}).get(check['name'], {})
        # A changed endpoint starts a new observation history.
        if previous.get('url') != check['url']:
            previous = {}
        error = results[check['name']]
        current = dict(previous, url=check['url'], checked_at=now)
        current['consecutive_failures'] = previous.get('consecutive_failures', 0) + 1 if error else 0
        should_alert = bool(error) and current['consecutive_failures'] >= config.get('failure_threshold', 3)
        should_alert = should_alert and (not previous.get('notified') or now - previous.get('last_sent', 0) >= config.get('repeat_seconds', 14400))
        recovery = not error and previous.get('notified', False)
        if should_alert or recovery:
            kind = 'RECOVERED' if recovery else 'ALERT'
            try:
                notify(config, f"[Deployer {kind}] {config['environment']}: {check['name']}",
                       f"Environment: {config['environment']}\nCheck: {check['name']}\nStatus: {error or 'HTTPS and TLS checks healthy'}\nObserved at Unix time: {now}\n")
            except Exception:
                failures.append(check['name'])
            else:
                current['notified'] = not recovery
                current['last_sent'] = now
        next_state['checks'][check['name']] = current
    return next_state, failures


def save_state(path, state):
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, delete=False) as output:
            temporary = Path(output.name)
            json.dump(state, output)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if temporary and temporary.exists():
            temporary.unlink()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--state-directory', type=Path)
    parser.add_argument('--check-only', action='store_true', help='Read-only probe; never send email or change state')
    args = parser.parse_args()
    os.umask(0o077)
    try:
        config = load_config(args.config)
        if args.check_only:
            results = {check['name']: probe(check) for check in config['checks']}
            print(json.dumps({'checks': {name: error or 'healthy' for name, error in results.items()}}))
            return int(any(results.values()))
        directory = args.state_directory
        if directory is None or not directory.is_absolute():
            raise ValueError('private state directory is required')
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        info = directory.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise ValueError('invalid state directory')
        with (directory / 'monitor.lock').open('a') as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                # A skipped run must be visible to systemd/host monitoring;
                # reporting success would create a false healthy signal.
                print('External monitor already running; this run was skipped')
                return 1
            path = directory / 'state.json'
            validate_smtp_config(config)
            state = json.loads(private_file(path).read_text()) if path.exists() else {}
            results = {check['name']: probe(check) for check in config['checks']}
            state, failures = update(config, state, results, int(time.time()))
            save_state(path, state)
            print('External checks complete; notification delivery failed' if failures else 'External checks complete')
            return 1 if failures else 0
    except Exception:
        print('External monitor failed; inspect protected configuration and connectivity')
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
