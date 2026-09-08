#!/usr/bin/env python3
"""Run generated provisioning shell against isolated files and fake host services."""
import hashlib
import importlib.util
import json
import os
import re
import shutil
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('provision_tests', ROOT / 'tests/provision-hosting-environment-test.py')
tests = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tests)
p = tests.provision


class ProvisionShellTests(unittest.TestCase):
    def test_complete_generated_sequence_and_verified_artifacts(self):
        self.execute_scenario()

    def test_bad_archive_checksum_stops_before_binary_install(self):
        self.execute_scenario(tamper=True)

    def execute_scenario(self, tamper=False):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            bindir = root / 'bin'
            bindir.mkdir()
            for directory in ['etc/systemd/system', 'var/lib', 'usr/local/bin', 'tmp']:
                (root / directory).mkdir(parents=True)
            def executable(path, contents):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(contents)
                path.chmod(0o755)
            fixtures = root / 'fixtures'
            package = fixtures / 'deployer-linux-amd64'
            executable(package / 'deployer-server', '''#!/usr/bin/env python3
import os, pathlib, sys
root=pathlib.Path(os.environ['TEST_ROOT'])
a=sys.argv[1:]
def value(flag): return a[a.index(flag)+1]
if a[:2]==['bootstrap','server']:
 path=pathlib.Path(value('--env-file')); path.parent.mkdir(parents=True,exist_ok=True); path.write_text('BOOTSTRAPPED=true\\n'); path.chmod(0o600)
 (root/'var/lib/deployer').mkdir(parents=True,exist_ok=True)
 (root/'var/lib/deployer/deployer.db').write_bytes(b'test-database')
 print('Admin token: dep_admin_synthetic')
elif a[:2]==['backup','create']:
 assert pathlib.Path(value('--database-path')).exists()
 pathlib.Path(value('--output')).write_bytes(b'test-encrypted-backup')
else: sys.exit(42)
''')
            executable(package / 'deployer', '#!/bin/sh\nexit 0\n')
            for script in ['auto-update.sh', 'install-release.sh']:
                executable(package / 'scripts' / script, '#!/bin/sh\nexit 99\n')
            unit = package / 'deploy/systemd/deployer-server.service'
            unit.parent.mkdir(parents=True)
            unit.write_text('[Service]\nExecStart=/usr/local/bin/deployer-server\n')
            archive = fixtures / 'release.tar.gz'
            with tarfile.open(archive, 'w:gz') as tar:
                tar.add(package, arcname=package.name)
            installer = fixtures / 'k3s-install.sh'
            installer.write_text('#!/bin/sh\n[ "$INSTALL_K3S_SKIP_DOWNLOAD" = true ] || exit 66\n')
            k3s = fixtures / 'k3s'
            executable(k3s, '''#!/usr/bin/env python3
import os,pathlib,sys
root=pathlib.Path(os.environ['TEST_ROOT'])
if sys.argv[-3:]==['apply','-f','-']: (root/'quota.json').write_text(sys.stdin.read())
''')
            manifest = tests.manifest()
            manifest['release']['artifact_sha256'] = hashlib.sha256(archive.read_bytes()).hexdigest()
            manifest['k3s']['installer_sha256'] = hashlib.sha256(installer.read_bytes()).hexdigest()
            manifest['k3s']['binary_sha256'] = hashlib.sha256(k3s.read_bytes()).hexdigest()
            downloads = {
                'https://github.com/0xivanov/self-hosted-deployer/releases/download/v0.1.0/deployer-linux-amd64.tar.gz': str(archive),
                'https://get.k3s.io': str(installer),
                'https://github.com/k3s-io/k3s/releases/download/v1.34.1+k3s1/k3s': str(k3s),
            }
            (root / 'downloads.json').write_text(json.dumps(downloads))
            executable(bindir / 'curl', '''#!/usr/bin/env python3
import json,os,pathlib,shutil,sys
root=pathlib.Path(os.environ['TEST_ROOT']); args=sys.argv[1:]
url=next(x for x in args if x.startswith('http'))
with (root/'curl-calls').open('a') as f: f.write(url+'\\n')
if url.startswith('http://127.0.0.1:7080/'): sys.exit(0)
shutil.copyfile(json.loads((root/'downloads.json').read_text())[url],args[args.index('-o')+1])
''')
            executable(bindir / 'sha256sum', '''#!/usr/bin/env python3
import hashlib,pathlib,sys
for line in sys.stdin:
 digest,path=line.strip().split(None,1)
 if hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()!=digest: sys.exit(1)
''')
            executable(bindir / 'openssl', '#!/usr/bin/env python3\nimport subprocess,sys\nif sys.argv[1] == "s_client": sys.exit(0)\nsys.exit(subprocess.call([' + repr(shutil.which('openssl')) + '] + sys.argv[1:]))\n')
            executable(bindir / 'apt-get', '#!/bin/sh\nexit 0\n')
            executable(bindir / 'systemctl', '#!/bin/sh\nprintf "%s\\n" "$*" >> "$TEST_ROOT/systemctl-calls"\n')
            executable(bindir / 'wg', '#!/bin/sh\ncase "$1" in genkey) echo synthetic-private;; pubkey) cat >/dev/null; echo synthetic-public;; *) exit 7;; esac\n')
            environment = dict(os.environ, TEST_ROOT=str(root), PATH=str(bindir)+os.pathsep+str(root/'usr/local/bin')+os.pathsep+os.environ['PATH'])
            if tamper:
                archive.write_bytes(archive.read_bytes() + b'changed')
            for index, command in enumerate(p.build_plan(manifest)[1:]):
                mapped = re.sub(r'/etc/|/var/|/usr/local/|/tmp/', lambda m: str(root) + m.group(), command)
                result = subprocess.run(['bash', '-c', mapped], env=environment, text=True, capture_output=True)
                if tamper and index == 1:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse((root/'usr/local/bin/deployer-server').exists())
                    return
                self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue((root/'var/lib/deployer/backups/platform-initial.enc').exists())
            config = (root/'etc/rancher/k3s/config.yaml').read_text()
            self.assertIn('cluster-cidr: 10.180.0.0/16', config)
            self.assertIn('service-cidr: 10.181.1.0/24', config)
            self.assertIn('cluster-dns: 10.181.1.10', config)
            self.assertIn('"0600"', config)
            quota = json.loads((root/'quota.json').read_text())
            self.assertEqual(quota['metadata']['namespace'], 'hosting-customer-a-prod')
            self.assertEqual(quota['spec']['hard']['limits.memory'], '1Gi')
            self.assertEqual((root/'etc/deployer/update-policy.conf').read_text(), 'mode=manual\n')
            self.assertIn('DEPLOYER_WIREGUARD_SUBNET=10.81.0.0/24', (root/'etc/deployer/server.env').read_text())
            self.assertIn('enable --now wg-quick@wg0.service', (root/'systemctl-calls').read_text())
            self.assertEqual(len((root/'curl-calls').read_text().splitlines()), 4)
            self.assertEqual((root/'etc/deployer/server.identity').stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
