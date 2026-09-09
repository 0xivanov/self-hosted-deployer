#!/usr/bin/env python3
"""Bounded memory exhaustion rehearsal inside the disposable replacement VM."""

import copy
import json
import os
import socket
import subprocess
import sys
import time
import uuid


def kube(*args, body=None):
    return subprocess.run(["k3s", "kubectl", *args], input=json.dumps(body) if body else None,
                          text=True, capture_output=True, check=True, timeout=150).stdout


def get(*args):
    return json.loads(kube("get", *args, "-o", "json"))


def healthy():
    result = subprocess.run(["curl", "-fsS", "--max-time", "3", "-H", "Host: app.deployer-poc.test",
                             "http://10.81.0.1/"], text=True, capture_output=True, check=True, timeout=5)
    assert result.stdout.strip() == "hosting-lab-ok"
    node = get("node", "lima-deployer-poc-lab")
    conditions = {c["type"]: c["status"] for c in node["status"]["conditions"]}
    assert conditions["Ready"] == "True" and conditions["MemoryPressure"] == "False"
    pods = get("pods", "-n", "hosting-mac-lab-a", "-l", "app.kubernetes.io/name=hosting-lab-app")["items"]
    assert len(pods) == 1 and pods[0]["spec"]["nodeName"] == "lima-deployer-poc-lab"
    statuses = pods[0]["status"]["containerStatuses"]
    assert all(c["ready"] for c in statuses)
    return pods[0]["metadata"]["uid"], [c["restartCount"] for c in statuses]


def main():
    if os.geteuid() != 0 or socket.gethostname() != "lima-deployer-poc-restore" or sys.argv[1:] != ["--apply"]:
        raise SystemExit("Requires root in lima-deployer-poc-restore and --apply")
    kube("rollout", "status", "deployment/hosting-lab-app", "-n", "hosting-mac-lab-a", "--timeout=120s")
    baseline = healthy()
    template = get("deployment", "hosting-lab-app", "-n", "hosting-mac-lab-a")["spec"]["template"]
    assert template["metadata"]["labels"]["deployer.io/hosting-profile"] == "v1"
    spec = copy.deepcopy(template["spec"])
    assert len(spec["containers"]) == 1 and not spec.get("volumes") and not spec.get("initContainers")
    container = spec["containers"][0]
    assert container["resources"]["limits"]["memory"] == "64Mi"
    assert container["securityContext"]["runAsNonRoot"] is True
    assert spec["automountServiceAccountToken"] is False
    for key in ("readinessProbe", "livenessProbe", "startupProbe", "env", "envFrom"):
        container.pop(key, None)
    container["imagePullPolicy"] = "Never"
    # PID 1 allocates rapidly beyond the inherited 64Mi limit. Never restart it.
    container["command"] = ["awk"]
    container["args"] = ['BEGIN { s="0123456789abcdef"; for (i=0; i<24; i++) s=s s; print length(s) }']
    spec["restartPolicy"] = "Never"
    spec["activeDeadlineSeconds"] = 60
    spec["nodeSelector"] = {"kubernetes.io/hostname": "lima-deployer-poc-lab"}
    namespace = "memory-qualification-" + uuid.uuid4().hex[:8]
    kube("create", "namespace", namespace)
    try:
        kube("apply", "-f", "-", body={"apiVersion": "v1", "kind": "Pod", "metadata": {
            "name": "exhaust-memory", "namespace": namespace}, "spec": spec})
        samples = 0
        for _ in range(45):
            assert healthy() == baseline, "Healthy neighbor changed during memory exhaustion"
            samples += 1
            statuses = get("pod", "exhaust-memory", "-n", namespace)["status"].get("containerStatuses", [])
            terminated = statuses[0]["state"].get("terminated") if statuses else None
            if terminated:
                assert terminated["reason"] == "OOMKilled" and terminated["exitCode"] == 137, terminated
                break
            time.sleep(2)
        else:
            raise AssertionError("Did not observe a container memory-limit OOM kill")
        for _ in range(5):
            assert healthy() == baseline, "Healthy neighbor changed after OOM kill"
            samples += 1
            time.sleep(2)
        print(f"PASS: 64Mi container OOMKilled; {samples} healthy ingress/node samples; neighbor UID/restarts unchanged")
    finally:
        kube("delete", "namespace", namespace, "--wait=true", "--timeout=120s")


if __name__ == "__main__":
    main()
