#!/usr/bin/env python3
"""Exercise the existing HTTP-01 solver against staging in a disposable namespace."""
import hashlib
import json
import os
import socket
import subprocess
import sys
import time
import uuid


def kube(*args, body=None):
    return subprocess.run(["k3s", "kubectl", *args], input=json.dumps(body) if body else None,
                          check=True, capture_output=True, text=True, timeout=90).stdout


def get(*args):
    return json.loads(kube("get", *args, "-o", "json"))


def live_state():
    cert = get("certificate", "money-manager-api-tls", "-n", "deployer-apps")
    secret = get("secret", cert["spec"]["secretName"], "-n", "deployer-apps")
    # Never print or persist private key material.
    public_hash = hashlib.sha256(secret["data"]["tls.crt"].encode()).hexdigest()
    return cert["metadata"]["uid"], cert["status"]["revision"], public_hash


def main():
    if os.geteuid() != 0 or socket.gethostname() != "v2202605362241463974" or sys.argv[1:] != ["--apply"]:
        raise SystemExit("Requires root on the known VPS and --apply")
    source = get("clusterissuer", "deployer-letsencrypt")["spec"]["acme"]
    expected = [{"http01": {"ingress": {"ingressClassName": "traefik"}}}]
    assert source["solvers"] == expected, "Review changed solver settings before testing"
    before = live_state()
    namespace = "certificate-qualification-" + uuid.uuid4().hex[:8]
    kube("create", "namespace", namespace)
    try:
        kube("apply", "-f", "-", body={"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
            "metadata": {"name": "staging", "namespace": namespace}, "spec": {"acme": {
                "email": source["email"], "server": "https://acme-staging-v02.api.letsencrypt.org/directory",
                "privateKeySecretRef": {"name": "staging-account"}, "solvers": source["solvers"]}}})
        kube("apply", "-f", "-", body={"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
            "metadata": {"name": "staging", "namespace": namespace}, "spec": {
                "secretName": "staging-tls", "dnsNames": ["money.0xivanov.dev"],
                "issuerRef": {"name": "staging", "kind": "Issuer"}}})
        ready = False
        for _ in range(90):
            cert = get("certificate", "staging", "-n", namespace)
            if any(c["type"] == "Ready" and c["status"] == "True" for c in cert.get("status", {}).get("conditions", [])):
                ready = True
                break
            time.sleep(2)
        assert ready, "Staging certificate did not become Ready within three minutes"
        assert live_state() == before, "Production certificate changed during staging test"
        print("PASS: staging HTTP-01 issuance; production certificate unchanged", flush=True)
        print("Verify public HTTPS separately from an external machine; this does not certify traffic health.", flush=True)
    finally:
        kube("delete", "namespace", namespace, "--wait=true", "--timeout=60s")


if __name__ == "__main__":
    main()
