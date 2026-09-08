#!/usr/bin/env python3
"""Real local restic round trip and checkpoint failure recovery; no network access."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
IMAGE = 'restic/restic@sha256:39d9072fb5651c80d75c7a811612eb60b4c06b32ffe87c2e9f3c7222e1797e76'
with tempfile.TemporaryDirectory(prefix='audit-e2e-') as temp:
    root = Path(temp).resolve()
    work = root / 'work'
    work.mkdir(mode=0o700)
    password = root / 'password'
    password.write_text('synthetic-audit-test-password')
    password.chmod(0o600)
    restic = root / 'restic'
    restic.write_text('#!' + sys.executable + '''
import os, sys
if os.environ.get('FAIL_DUMP') and 'dump' in sys.argv: sys.exit(1)
os.execvp('docker', ['docker', 'run', '--rm', '-i', '--network', 'none',
 '--user', f'{os.getuid()}:{os.getgid()}', '-v', os.environ['TEST_ROOT']+':'+os.environ['TEST_ROOT'],
 '-e', 'RESTIC_REPOSITORY', '-e', 'RESTIC_PASSWORD_FILE', os.environ['TEST_IMAGE'], *sys.argv[1:]])
''')
    restic.chmod(0o755)
    env = {k: v for k, v in os.environ.items() if not k.startswith(('RESTIC_', 'AWS_'))}
    env.update(RESTIC_REPOSITORY=str(root/'repository'), RESTIC_PASSWORD_FILE=str(password), TEST_ROOT=str(root), TEST_IMAGE=IMAGE)
    subprocess.run([str(restic), '--no-cache', 'init'], env=env, check=True, capture_output=True)
    source, checkpoint = root/'audit.jsonl', root/'checkpoint'
    fixture = (ROOT/'tests/audit-e2e-fixture.jsonl').read_text()
    source.write_text(fixture)
    source.chmod(0o600)
    command = ['python3', str(ROOT/'scripts/offsite-audit-export.py'), '--audit-log', str(source),
               '--checkpoint', str(checkpoint), '--work-root', str(work), '--restic-binary', str(restic), '--environment', 'test-pilot']
    first = subprocess.run(command, env=env, capture_output=True, text=True)
    assert first.returncode == 0, first.stderr
    saved = checkpoint.read_bytes()
    assert json.loads(saved)['offset'] == len(fixture.encode())
    assert not list(work.iterdir())
    # Incomplete writes must never become a committed cursor boundary.
    record = json.loads(fixture.splitlines()[-1])
    record['target']['app'] = 'second'
    payload = json.dumps(record)
    with source.open('a') as output: output.write(payload[:20])
    partial = subprocess.run(command, env=env, capture_output=True)
    assert partial.returncode == 0 and checkpoint.read_bytes() == saved
    with source.open('a') as output: output.write(payload[20:]+'\n')
    failed = subprocess.run(command, env=dict(env, FAIL_DUMP='1'), capture_output=True)
    assert failed.returncode != 0 and checkpoint.read_bytes() == saved
    second = subprocess.run(command, env=env, capture_output=True, text=True)
    assert second.returncode == 0, second.stderr
    sid = second.stdout.split('snapshot=')[1].split()[0]
    restored = subprocess.run([str(restic), '--no-cache', 'dump', sid, '/audit.jsonl'], env=env, capture_output=True, check=True)
    exported = [json.loads(line) for line in restored.stdout.splitlines()]
    assert len(exported) == 1 and exported[0]['target']['app'] == 'second'
    assert 'msg' not in exported[0] and 'level' not in exported[0]
    assert json.loads(checkpoint.read_bytes())['offset'] == source.stat().st_size
    source.write_text('changed history\n')
    assert subprocess.run(command, env=env, capture_output=True).returncode != 0
print('Real audit restic export, partial line and failed verification recovery passed')
