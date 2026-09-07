#!/bin/sh
# Read-only inventory. No credentials, Secret objects, or environment files.
set -eu

if [ "$#" -ne 0 ]; then
  echo "Usage: hosting-inventory.sh (run locally on each Linux node)" >&2
  exit 2
fi

printf 'Inventory UTC: '
date -u '+%Y-%m-%dT%H:%M:%SZ'
uname -sm
for binary in deployer deployer-server deployer-agent; do
  if command -v "$binary" >/dev/null 2>&1; then
    "$binary" version
  fi
done

if command -v systemctl >/dev/null 2>&1; then
  for unit in deployer-server.service deployer-agent.service \
    deployer-auto-update-server.timer deployer-auto-update-agent.timer \
    deployer-auto-update-server.service deployer-auto-update-agent.service \
    k3s.service k3s-agent.service; do
    # Explicit properties avoid dumping ExecStart arguments or environment data.
    systemctl show "$unit" --property=Id,LoadState,ActiveState,SubState,UnitFileState
  done
fi

df -Pk / /var/lib
if command -v k3s >/dev/null 2>&1; then
  k3s --version
  if k3s kubectl get nodes -o name >/dev/null 2>&1; then
    k3s kubectl get nodes -o wide
    k3s kubectl get deployments -A \
      -o 'custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,GENERATION:.metadata.generation,DESIRED:.spec.replicas,AVAILABLE:.status.availableReplicas'
    k3s kubectl get pods -A \
      -o 'custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,UID:.metadata.uid,NODE:.spec.nodeName,PHASE:.status.phase'
    k3s kubectl get pvc -A \
      -o 'custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,VOLUME:.spec.volumeName,CLASS:.spec.storageClassName,STATUS:.status.phase,CAPACITY:.status.capacity.storage'
    k3s kubectl get ingress -A \
      -o 'custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,HOSTS:.spec.rules[*].host'
  else
    echo 'Cluster inventory unavailable here; collect it on the server with authorized Kubernetes access.'
  fi
fi
