import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("provision", ROOT / "scripts/provision-hosting-environment.py")
provision = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = provision
SPEC.loader.exec_module(provision)


def manifest(env="customer-a-prod"):
    return {
        "environment": {"id": env}, "customer": {"id": "customer-a", "label": "A"},
        "ssh": {"host": "198.51.100.20", "user": "root"},
        "release": {"version": "v0.1.0", "asset": "deployer-linux-amd64.tar.gz", "installer_sha256": "0" * 64, "artifact_sha256": "1" * 64},
        "network": {"wireguard_cidr": "10.81.0.0/24", "wireguard_hub_ip": "10.81.0.1", "pod_cidr": "10.180.0.0/16", "service_cidr": "10.181.1.0/24", "wireguard_interface": "wg0", "wireguard_endpoint": "198.51.100.20:51820"},
        "k3s": {"version": "v1.34.1+k3s1", "installer_url": "https://get.k3s.io", "installer_sha256": "2" * 64, "binary_sha256": "3" * 64},
        "domain": {"base": "a.example"}, "admin_endpoint": "https://admin.a.example:7443",
        "tls": {"cert_path": "/etc/ssl/deployer/fullchain.pem", "key_path": "/etc/ssl/deployer/privkey.pem"},
        "resource_budget": {"cpu": "4", "memory": "1Gi", "ephemeral_storage": "1Gi", "pods": 10}, "backup": {"scope": "platform-database", "destination": "local-platform-backup"}, "recovery": {"targets": ["rpo"]},
    }


class Runner(provision.Runner):
    def __init__(self, inventory): self.inventory, self.commands = inventory, []
    def run(self, command, *, capture=True):
        self.commands.append((command, capture))
        return self.inventory if len(self.commands) == 1 else ""


class ProvisionTests(unittest.TestCase):
    def test_plan_is_read_only(self):
        runner = Runner("deployer-provisioning-inventory-v1\nLinux 6.1\ninit=systemd\n")
        with tempfile.TemporaryDirectory() as d:
            result = provision.run_provision(manifest(), runner, Path(d), apply=False)
            self.assertEqual(result["status"], "planned")
        self.assertEqual(len(runner.commands), 2)

    def test_apply_uses_each_environment_endpoint(self):
        first, second = manifest("customer-a-prod"), manifest("customer-b-prod")
        second["admin_endpoint"] = "https://admin.b.example:7443"
        self.assertNotEqual(provision.build_plan(first), provision.build_plan(second))
        runner = Runner("deployer-provisioning-inventory-v1\nLinux 6.1\ninit=systemd\n")
        with tempfile.TemporaryDirectory() as d:
            result = provision.run_provision(first, runner, Path(d), apply=True)
            self.assertEqual(result["status"], "applied")
            record = json.loads((Path(d) / "customer-a-prod-install-record.json").read_text())
            self.assertNotIn("dep_admin_", json.dumps(record))
        self.assertGreater(len(runner.commands), 1)

    def test_existing_node_refused_before_mutation(self):
        runner = Runner("path=/etc/rancher/k3s/config.yaml\n")
        with self.assertRaises(provision.ProvisioningError):
            provision.run_provision(manifest(), runner, Path(tempfile.mkdtemp()), apply=True)
        self.assertEqual(len(runner.commands), 1)

    def test_network_registry_overlap_and_separation(self):
        with self.assertRaises(provision.ProvisioningError):
            provision.validate_manifest(manifest(), [{"environment_id": "other", "wireguard_cidr": "10.81.0.0/25", "pod_cidr": "10.190.0.0/16", "service_cidr": "10.190.1.0/24"}])
        provision.validate_manifest(manifest("customer-b-prod"), [{"environment_id": "other", "wireguard_cidr": "10.82.0.0/24", "pod_cidr": "10.190.0.0/16", "service_cidr": "10.190.1.0/24"}])

    def test_failfast_and_redaction(self):
        with self.assertRaises(provision.ProvisioningError):
            provision.validate_manifest({})
        redacted = provision.redact("Admin token: dep_admin_abc secret=x password=hello")
        self.assertNotIn("dep_admin_abc", redacted)
        self.assertNotIn("hello", redacted)

    def test_existing_loaded_unit_and_duplicate_registry_fail(self):
        with self.assertRaises(provision.ProvisioningError):
            provision.refuse_existing_node("Id=k3s.service\nLoadState=loaded\nActiveState=inactive\n")
        with self.assertRaises(provision.ProvisioningError):
            provision.validate_manifest(manifest(), [{"environment_id": "customer-a-prod", "wireguard_cidr": "10.82.0.0/24", "pod_cidr": "10.190.0.0/16", "service_cidr": "10.190.1.0/24"}])

    def test_ssh_does_not_send_separator_and_uses_sudo(self):
        with mock.patch.object(provision.subprocess, "run") as run:
            run.return_value = mock.Mock(returncode=0, stdout="ok")
            provision.SSHRunner("host", "operator").run("true")
            argv = run.call_args.args[0]
            self.assertNotIn("--", argv)
            self.assertIn("sudo -n bash -lc", argv[-1])

    def test_private_release_origin_requires_https_and_keeps_hash_verification(self):
        config = manifest()
        config['release']['base_url'] = 'https://artifacts.example.test/pilot/'
        provision.validate_manifest(config, [])
        install = provision.build_plan(config)[2]
        self.assertIn('https://artifacts.example.test/pilot/deployer-linux-amd64.tar.gz', install)
        self.assertIn(config['release']['artifact_sha256'], install)
        for value in ['', None, 123, 'http://example.test', 'https://user:password@example.test', 'https://example.test?token=secret']:
            config['release']['base_url'] = value
            with self.assertRaises(provision.ProvisioningError):
                provision.validate_manifest(config, [])

    def test_generated_mutations_are_bash_parseable(self):
        for command in provision.build_plan(manifest())[1:]:
            checked = subprocess.run(["bash", "-n"], input=command, text=True, capture_output=True)
            self.assertEqual(checked.returncode, 0, checked.stderr + "\n" + command)


if __name__ == "__main__":
    unittest.main()
