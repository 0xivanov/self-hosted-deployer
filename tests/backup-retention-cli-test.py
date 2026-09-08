#!/usr/bin/env python3
"""Exercise deletion boundaries through the actual retention CLI."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/backup-retention.py'


class RetentionCLITests(unittest.TestCase):
    def run_case(self, extra=(), fail_forget=False, kind="platform"):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            snapshots = [
                {'id': letter * 64, 'time': date, 'tags': tags}
                for letter, date, tags in [
                    ('a', '2026-09-07T01:00:00Z', ['pilot', kind]),
                    ('b', '2026-09-07T00:00:00Z', ['pilot', kind]),
                    ('c', '2026-09-06T00:00:00Z', ['other', 'platform']),
                    ('d', '2026-09-06T00:00:00Z', ['pilot', 'recovery']),
                    ('e', '2026-09-06T00:00:00Z', ['pilot', 'platform', 'rollback']),
                    ('f', '2026-01-01T00:00:00Z', ['pilot', 'audit'] if kind == 'audit' else ['pilot']),
                    ('0', '2026-09-08T00:00:00Z', ['pilot', 'platform']),
                ]
            ]
            (root / 'snapshots.json').write_text(json.dumps(snapshots))
            password = root / 'password'
            password.write_text('test-repository-password')
            password.chmod(0o600)
            binary = root / 'restic'
            binary.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
root = pathlib.Path(__file__).parent
args = sys.argv[1:]
with (root / 'calls').open('a') as f: f.write(json.dumps(args)+'\\n')
if 'snapshots' in args: print((root / 'snapshots.json').read_text())
if 'forget' in args and os.environ.get('FAIL_FORGET'): sys.exit(1)
''')
            binary.chmod(0o755)
            env = {k: v for k, v in os.environ.items() if not k.startswith(('RESTIC_', 'AWS_'))}
            env.update(RESTIC_REPOSITORY=str(root / 'repository'), RESTIC_PASSWORD_FILE=str(password))
            if fail_forget:
                env['FAIL_FORGET'] = '1'
            result = subprocess.run(['python3', str(SCRIPT), '--restic-binary', str(binary),
                '--environment', 'pilot', '--backup-kind', kind,
                *(['--audit-keep-days', '90'] if kind == 'audit' else ['--daily', '1', '--weekly', '1', '--monthly', '1']), '--now', '2026-09-07T12:00:00Z', *extra], env=env, capture_output=True, text=True)
            calls = [json.loads(line) for line in (root / 'calls').read_text().splitlines()] if (root / 'calls').exists() else []
            return result, calls

    def test_default_only_lists_and_scopes_candidates(self):
        result, calls = self.run_case()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, [['--no-cache', 'snapshots', '--json']])
        self.assertEqual(json.loads(result.stdout)['eligible'], ['b' * 64])

    def test_apply_only_forgets_explicit_scoped_ids(self):
        result, calls = self.run_case(['--apply'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls[-1], ['--no-cache', 'forget', 'b' * 64])
        self.assertEqual(len(calls), 2)

    def test_failed_forget_does_not_prune(self):
        result, calls = self.run_case(['--apply', '--prune'], fail_forget=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('prune' in call for call in calls))

    def test_audit_age_policy_keeps_every_recent_batch(self):
        result, calls = self.run_case(kind='audit')
        self.assertEqual(result.returncode, 0, result.stderr)
        plan = json.loads(result.stdout)
        self.assertEqual(set(plan['keep']), {'a'*64, 'b'*64})
        self.assertEqual(plan['eligible'], ['f'*64])
        self.assertEqual(len(calls), 1)

    def test_incremental_audit_retention_is_refused_before_restic(self):
        result, calls = self.run_case(['--backup-kind', 'audit', '--apply'])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])


if __name__ == '__main__':
    unittest.main()
