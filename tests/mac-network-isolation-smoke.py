#!/usr/bin/env python3
"""Run inside the disposable replacement Mac VM, using its deployed policy."""

import json
import os
import socket
import subprocess
import sys
import time
import uuid


def kube(*args, body=None, check=True):
    return subprocess.run(
        ["k3s", "kubectl", *args], input=json.dumps(body) if body else None,
        text=True, capture_output=True, check=check, timeout=150,
    )


def get(*args):
    return json.loads(kube("get", *args, "-o", "json").stdout)


def apply(obj):
    kube("apply", "-f", "-", body=obj)


def probe(namespace, target):
    return kube("exec", "-n", namespace, "probe", "--", "wget", "-T", "3", "-qO-",
                f"http://{target}:8080/", check=False)


def allowed(namespace, target):
    result = probe(namespace, target)
    assert result.returncode == 0 and result.stdout.strip() == "hosting-lab-ok", result.stderr


def denied(namespace, target):
    result = probe(namespace, target)
    assert result.returncode != 0, f"Unexpected access to {target}"
    assert any(message in result.stderr for message in (
        "timed out", "No route to host", "Host is unreachable", "Connection refused"
    )), f"Unexpected probe error: {result.stderr}"


def ingress(host):
    result = subprocess.run(["curl", "-fsS", "--max-time", "5", "-H", f"Host: {host}",
                             "http://10.81.0.1/"], text=True, capture_output=True, timeout=10)
    return result.returncode == 0 and result.stdout.strip() == "hosting-lab-ok"


def app_state():
    pods = get("pods", "-n", "hosting-mac-lab-a", "-l", "app.kubernetes.io/name=hosting-lab-app")["items"]
    assert pods, "Existing lab app is missing"
    return sorted((pod["metadata"]["uid"], tuple(c["restartCount"] for c in pod["status"]["containerStatuses"])) for pod in pods)


def main():
    if os.geteuid() != 0 or socket.gethostname() != "lima-deployer-poc-restore" or sys.argv[1:] != ["--apply"]:
        raise SystemExit("Requires root in lima-deployer-poc-restore and --apply")
    kube("wait", "--for=condition=Ready", "node/lima-deployer-poc-lab", "node/mac-lab-worker", "--timeout=120s")
    source = get("networkpolicy", "hosting-lab-app", "-n", "hosting-mac-lab-a")
    assert source["spec"]["policyTypes"] == ["Ingress", "Egress"]
    labels = source["spec"]["podSelector"]["matchLabels"]
    assert labels["deployer.io/hosting-profile"] == "v1"
    suffix = uuid.uuid4().hex[:8]
    baseline = app_state()
    protected, other = f"isolation-hosted-{suffix}", f"isolation-other-{suffix}"
    created = []
    try:
        for namespace, node in ((protected, "mac-lab-worker"), (other, "lima-deployer-poc-lab")):
            # Create exclusively; cleanup can only delete namespaces we created.
            kube("create", "namespace", namespace)
            created.append(namespace)
            apply({"apiVersion": "v1", "kind": "Pod", "metadata": {
                "name": "probe", "namespace": namespace, "labels": labels}, "spec": {
                "nodeSelector": {"kubernetes.io/hostname": node},
                "automountServiceAccountToken": False,
                "securityContext": {"runAsNonRoot": True, "runAsUser": 65532},
                "containers": [{"name": "probe", "image": "docker.io/library/hosting-lab-app:qualification",
                    "imagePullPolicy": "Never", "resources": {"requests": {"cpu": "10m", "memory": "8Mi"},
                    "limits": {"cpu": "100m", "memory": "32Mi"}}}]}})
            apply({"apiVersion": "v1", "kind": "Service", "metadata": {"name": "probe", "namespace": namespace},
                   "spec": {"selector": labels, "ports": [{"port": 8080, "targetPort": 8080}]}})
        route_host = f"isolation-{suffix}.deployer-poc.test"
        apply({"apiVersion": "networking.k8s.io/v1", "kind": "Ingress",
               "metadata": {"name": "qualification", "namespace": protected}, "spec": {
                   "ingressClassName": "traefik", "rules": [{"host": route_host, "http": {"paths": [{
                       "path": "/", "pathType": "Prefix", "backend": {"service": {
                           "name": "probe", "port": {"number": 8080}}}}]}}]}})
        targets = {}
        for namespace in created:
            kube("wait", "--for=condition=Ready", "pod/probe", "-n", namespace, "--timeout=120s")
            targets[namespace] = [get("pod", "probe", "-n", namespace)["status"]["podIP"],
                                  get("service", "probe", "-n", namespace)["spec"]["clusterIP"]]
        paths = [(other, target) for target in targets[protected]] + [(protected, target) for target in targets[other]]
        for namespace, target in paths:
            allowed(namespace, target)
        policy = {"apiVersion": source["apiVersion"], "kind": source["kind"],
                  "metadata": {"name": "qualification", "namespace": protected}, "spec": source["spec"]}
        apply(policy)
        time.sleep(5)
        for namespace, target in paths:
            denied(namespace, target)
        kube("exec", "-n", protected, "probe", "--", "nslookup", "kubernetes.default.svc.cluster.local")
        for _ in range(15):
            if ingress(route_host):
                break
            time.sleep(2)
        else:
            raise AssertionError("Protected cross-node app is unreachable through Traefik")
        print("PASS: cross-node Pod IP and Service IP ingress/egress denied; DNS allowed", flush=True)
        print("PASS: real Traefik ingress reaches the protected worker app", flush=True)
        kube("delete", "networkpolicy", "qualification", "-n", protected)
        time.sleep(5)
        for namespace, target in paths:
            allowed(namespace, target)
        print("PASS: all four paths work before policy and after removal", flush=True)
        assert ingress("app.deployer-poc.test")
        assert app_state() == baseline, "Existing app Pod identity or restart count changed"
        print("PASS: existing app serves through Traefik with unchanged Pods and restart counts", flush=True)
    finally:
        for namespace in created:
            kube("delete", "namespace", namespace, "--wait=true", "--timeout=120s")


if __name__ == "__main__":
    main()
