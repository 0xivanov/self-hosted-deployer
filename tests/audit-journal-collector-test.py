#!/usr/bin/env python3
"""Exercise collection, resume and missing-history behavior via a real child process."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/collect-audit-journal.py'


class CollectorTests(unittest.TestCase):
    def test_timer_invokes_export_with_collection_beforehand(self):
        units = SCRIPT.parents[1] / 'deploy/systemd'
        service = (units / 'deployer-audit-export.service').read_text()
        timer = (units / 'deployer-audit-export.timer').read_text()
        self.assertIn('Unit=deployer-audit-export.service', timer)
        self.assertIn('ExecStartPre=/usr/bin/python3 /usr/local/libexec/collect-audit-journal.py', service)
        self.assertNotIn('Requires=deployer-audit-journal', service)
        self.assertNotIn('systemctl start', (units / 'deployer-audit-journal.service').read_text())

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.output, self.cursor = self.root / 'audit.jsonl', self.root / 'cursor'
        self.binary = self.root / 'journalctl'
        self.binary.write_text('''#!/usr/bin/env python3
import json, pathlib, sys
root=pathlib.Path(__file__).parent
records=json.loads((root/'records').read_text())
if '--cursor' in sys.argv:
    cursor=sys.argv[sys.argv.index('--cursor')+1]
    matches=[i for i,r in enumerate(records) if r['__CURSOR']==cursor]
    if matches: records=records[matches[0]:]
for item in records: print(json.dumps(item))
if (root/'fail').exists(): sys.exit(1)
''')
        self.binary.chmod(0o700)
        self.env = dict(os.environ, PATH=str(self.root) + os.pathsep + os.environ['PATH'])

    def record(self, cursor, app):
        inner = {'level': 'INFO', 'msg': 'mutation audit', 'timestamp': '2026-09-07T00:00:00Z',
                 'correlation_id': 'a'*32, 'token_id': 'b'*32, 'caller_kind': 'admin', 'node_id': '',
                 'method': '/deployer.v1.AppService/DeleteApp', 'target': {'app': app}, 'outcome': 'OK'}
        return {'__CURSOR': cursor, 'MESSAGE': json.dumps(inner)}

    def invoke(self, records):
        (self.root / 'records').write_text(json.dumps(records))
        return subprocess.run(['python3', str(SCRIPT), '--output', str(self.output), '--cursor', str(self.cursor)],
                              env=self.env, capture_output=True, text=True)

    def test_resume_retains_history_without_duplicate_boundary(self):
        first, second = self.record('c1', 'first'), self.record('c2', 'second')
        self.assertEqual(self.invoke([first]).returncode, 0)
        self.assertEqual(self.invoke([first, second]).returncode, 0)
        self.assertEqual(self.invoke([first, second]).returncode, 0)
        records = [json.loads(x) for x in self.output.read_text().splitlines()]
        self.assertEqual([x['target']['app'] for x in records], ['first', 'second'])
        self.assertEqual(self.cursor.read_text().strip(), 'c2')
        self.assertEqual(self.output.stat().st_mode & 0o777, 0o600)

    def test_vacuumed_cursor_fails_and_does_not_advance(self):
        self.assertEqual(self.invoke([self.record('c1', 'first')]).returncode, 0)
        self.assertNotEqual(self.invoke([self.record('c2', 'second')]).returncode, 0)
        self.assertEqual(self.cursor.read_text().strip(), 'c1')
        self.assertEqual(len(self.output.read_text().splitlines()), 1)

    def test_empty_initial_journal_creates_private_empty_output(self):
        self.assertEqual(self.invoke([]).returncode, 0)
        self.assertEqual(self.output.read_bytes(), b'')
        self.assertEqual(self.output.stat().st_mode & 0o777, 0o600)
        self.assertFalse(self.cursor.exists())

    def test_query_failure_keeps_cursor_for_retry(self):
        self.assertEqual(self.invoke([self.record('c1', 'first')]).returncode, 0)
        (self.root / 'fail').touch()
        self.assertNotEqual(self.invoke([self.record('c1', 'first'), self.record('c2', 'second')]).returncode, 0)
        self.assertEqual(self.cursor.read_text().strip(), 'c1')

    def test_symlink_output_is_refused(self):
        target = self.root / 'target'
        target.write_text('untouched')
        self.output.symlink_to(target)
        self.assertNotEqual(self.invoke([]).returncode, 0)
        self.assertEqual(target.read_text(), 'untouched')


if __name__ == '__main__':
    unittest.main()
