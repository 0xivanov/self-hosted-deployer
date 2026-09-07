#!/usr/bin/env python3
"""Mocked failure checks, plus --real for an isolated SQLite/restic rehearsal."""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent
JOB = ROOT / 'scripts/backup-platform-offsite.sh'
SNAPSHOT = 'a' * 64
RESTIC_IMAGE = 'restic/restic@sha256:39d9072fb5651c80d75c7a811612eb60b4c06b32ffe87c2e9f3c7222e1797e76'


def executable(path, source):
    path.write_text('#!' + sys.executable + '\n' + source)
    path.chmod(0o700)


with tempfile.TemporaryDirectory(prefix='platform-backup-job-test-') as directory:
    root = Path(directory).resolve()
    work = root / 'work'
    work.mkdir(mode=0o700)
    key = root / 'backup.key'
    key.write_bytes(os.urandom(32))
    key.chmod(0o600)
    password = root / 'restic.password'
    password.write_text('synthetic-local-test-password')
    password.chmod(0o600)
    database = root / 'source.db'
    with sqlite3.connect(database) as conn:
        conn.execute('create table evidence (value text)')
        conn.execute('insert into evidence values (?)', ('synthetic recovery evidence',))
    server, restic = root / 'server', root / 'restic'
    env = {k: v for k, v in os.environ.items() if not k.startswith('RESTIC_')}
    env.update(RESTIC_REPOSITORY=str(root / 'repository'), RESTIC_PASSWORD_FILE=str(password),
               TEST_ROOT=str(root), TEST_SNAPSHOT=SNAPSHOT, TEST_IMAGE=RESTIC_IMAGE)
    real = '--real' in sys.argv
    if real:
        subprocess.run(['go', 'build', '-o', str(server), './cmd/deployer-server'], cwd=ROOT, check=True)
        executable(restic, '''import os, sys
os.execvp('docker', ['docker', 'run', '--rm', '-i', '--network', 'none',
 '--user', f'{os.getuid()}:{os.getgid()}', '-v', os.environ['TEST_ROOT']+':'+os.environ['TEST_ROOT'],
 '-e', 'RESTIC_REPOSITORY', '-e', 'RESTIC_PASSWORD_FILE', os.environ['TEST_IMAGE'], *sys.argv[1:]])
''')
        # Only the test initializes a brand-new local repository. The job never does.
        subprocess.run([str(restic), '--no-cache', 'init'], env=env, check=True, stdout=subprocess.DEVNULL)
    else:
        executable(server, '''import os, pathlib, sys
if os.environ.get('TEST_MODE') == 'create-fail':
 print('secret-marker', file=sys.stderr); sys.exit(1)
pathlib.Path(sys.argv[sys.argv.index('--output')+1]).write_bytes(b'synthetic encrypted artifact')
''')
        executable(restic, '''import json, os, pathlib, sys
args=sys.argv[1:]
assert args.pop(0)=='--no-cache'
cmd=args.pop(0); mode=os.environ.get('TEST_MODE', '')
root=pathlib.Path(os.environ['TEST_ROOT']); sid=os.environ['TEST_SNAPSHOT']
with (root/'calls').open('a') as log: log.write(cmd+'\\n')
if mode == cmd+'-fail':
 print('secret-marker', file=sys.stderr); sys.exit(1)
if cmd=='backup':
 assert '--stdin' in args and args[args.index('--stdin-filename')+1]=='platform.backup'
 assert args[args.index('--tag')+1]=='pilot-a'
 (root/'uploaded').write_bytes(sys.stdin.buffer.read())
 if mode=='malformed-summary': print('not-json')
 elif mode!='missing-summary': print(json.dumps({'message_type':'summary','snapshot_id':sid}))
elif cmd=='snapshots':
 assert args==['--json',sid]
 print(json.dumps([{'id':sid, 'tags':['wrong' if mode=='wrong-tag' else 'pilot-a']}]))
elif cmd=='dump':
 assert args==[sid,'/platform.backup']
 sys.stdout.buffer.write(b'wrong bytes' if mode=='mismatch' else (root/'uploaded').read_bytes())
else: raise AssertionError('unexpected restic operation')
''')
    args = [str(JOB), '--server-binary', str(server), '--restic-binary', str(restic),
            '--database-path', str(database), '--backup-key-file', str(key),
            '--work-root', str(work), '--environment', 'pilot-a']
    modes = ['success'] if real else ['success', 'create-fail', 'backup-fail', 'snapshots-fail',
                                    'dump-fail', 'mismatch', 'missing-summary', 'malformed-summary', 'wrong-tag',
                                    'missing-repository', 'conflicting-password', 'unsafe-password',
                                    'unsafe-work-root', 'invalid-tag']
    for mode in modes:
        case_env = dict(env, TEST_MODE=mode)
        case_args = args.copy()
        if mode == 'missing-repository': case_env.pop('RESTIC_REPOSITORY')
        if mode == 'conflicting-password': case_env['RESTIC_PASSWORD'] = 'secret-marker'
        if mode == 'unsafe-password': password.chmod(0o644)
        if mode == 'unsafe-work-root': work.chmod(0o755)
        if mode == 'invalid-tag': case_args[-1] = 'pilot-a\nsecret-marker'
        result = subprocess.run(case_args, env=case_env, capture_output=True, text=True)
        password.chmod(0o600)
        work.chmod(0o700)
        assert (result.returncode == 0) == (mode == 'success'), (mode, result.stdout, result.stderr)
        assert ('repository round-trip verified' in result.stdout) == (mode == 'success'), mode
        assert 'secret-marker' not in result.stdout + result.stderr, mode
        assert not list(work.iterdir()), f'{mode}: temporary artifacts leaked'
    if real:
        # Inspect the exact snapshot reported by the real run, never "latest".
        sid = result.stdout.strip().split('snapshot=')[1]
        artifact = root / 'retrieved.backup'
        with artifact.open('wb') as output:
            subprocess.run([str(restic), '--no-cache', 'dump', sid, '/platform.backup'], env=env, stdout=output, check=True)
        recovered = root / 'restored.db'
        subprocess.run([str(server), 'backup', 'restore', '--input', str(artifact), '--output', str(recovered), '--key-file', str(key)], check=True, stdout=subprocess.DEVNULL)
        with sqlite3.connect(recovered) as conn:
            assert conn.execute('select value from evidence').fetchall() == [('synthetic recovery evidence',)]
    print('platform backup job tests passed (' + ('real SQLite and isolated restic' if real else 'mocked failure cases') + ')')
