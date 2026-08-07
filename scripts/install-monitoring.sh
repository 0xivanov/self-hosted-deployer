#!/bin/sh
set -eu

CLIENT="auto"
TIMEOUT="5m"
ALERTMANAGER_CONFIG=""
NAMESPACE="deployer-monitoring"
SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
STACK_MANIFEST="$SCRIPT_DIR/../deploy/monitoring/stack.yaml"

usage() {
  cat >&2 <<'USAGE'
Usage: install-monitoring.sh [--client auto|k3s|kubectl] [--timeout DURATION] [--alertmanager-config FILE]

Installs the deployer's private Prometheus, Alertmanager, and kube-state-metrics
stack. The monitoring workloads are pinned to the k3s control-plane node and
are not exposed through an Ingress.

Options:
  --client CLIENT              Kubernetes client: auto, k3s, or kubectl (default: auto)
  --timeout DURATION           Rollout timeout (default: 5m)
  --alertmanager-config FILE   Alertmanager YAML to store as a Kubernetes Secret
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --client)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--client requires auto, k3s, or kubectl" >&2
        usage
        exit 2
      fi
      CLIENT="$2"
      shift 2
      ;;
    --timeout)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--timeout requires a duration such as 5m" >&2
        usage
        exit 2
      fi
      TIMEOUT="$2"
      shift 2
      ;;
    --alertmanager-config)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--alertmanager-config requires a file" >&2
        usage
        exit 2
      fi
      ALERTMANAGER_CONFIG="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

case "$CLIENT" in
  auto|k3s|kubectl)
    ;;
  *)
    echo "unsupported Kubernetes client: $CLIENT" >&2
    usage
    exit 2
    ;;
esac

if [ ! -f "$STACK_MANIFEST" ]; then
  echo "monitoring manifest not found: $STACK_MANIFEST" >&2
  exit 1
fi
if [ -n "$ALERTMANAGER_CONFIG" ] && [ ! -s "$ALERTMANAGER_CONFIG" ]; then
  echo "Alertmanager config is missing or empty: $ALERTMANAGER_CONFIG" >&2
  exit 1
fi

if [ "$CLIENT" = "auto" ]; then
  if [ "$(id -u)" = "0" ] && command -v k3s >/dev/null 2>&1; then
    CLIENT="k3s"
  elif command -v kubectl >/dev/null 2>&1; then
    CLIENT="kubectl"
  elif command -v k3s >/dev/null 2>&1; then
    echo "k3s was found but this script is not root; rerun with sudo or provide a configured kubectl" >&2
    exit 1
  else
    echo "k3s or kubectl is required" >&2
    exit 1
  fi
fi

case "$CLIENT" in
  k3s)
    if [ "$(id -u)" != "0" ]; then
      echo "--client k3s must run as root; rerun with sudo" >&2
      exit 1
    fi
    kubectl_run() {
      k3s kubectl "$@"
    }
    ;;
  kubectl)
    kubectl_run() {
      kubectl "$@"
    }
    ;;
esac

WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/deployer-monitoring.XXXXXX")
cleanup() {
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

kubectl_run apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $NAMESPACE
  labels:
    app.kubernetes.io/managed-by: deployer
EOF

config_source="$ALERTMANAGER_CONFIG"
if [ -z "$config_source" ] && ! kubectl_run -n "$NAMESPACE" get secret alertmanager-config >/dev/null 2>&1; then
  config_source="$WORKDIR/alertmanager.yml"
  cat >"$config_source" <<'EOF'
route:
  receiver: discard
  group_by: [alertname, app, severity]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
receivers:
  - name: discard
EOF
fi

if [ -n "$config_source" ]; then
  if command -v amtool >/dev/null 2>&1; then
    amtool check-config "$config_source"
  fi
  kubectl_run -n "$NAMESPACE" create secret generic alertmanager-config \
    --from-file="alertmanager.yml=$config_source" \
    --dry-run=client -o yaml | kubectl_run apply -f -
fi

kubectl_run apply --server-side --field-manager=deployer-monitoring-installer -f "$STACK_MANIFEST"
kubectl_run -n "$NAMESPACE" rollout restart deployment/prometheus deployment/alertmanager
kubectl_run -n "$NAMESPACE" rollout status deployment/prometheus --timeout="$TIMEOUT"
kubectl_run -n "$NAMESPACE" rollout status deployment/alertmanager --timeout="$TIMEOUT"
kubectl_run -n "$NAMESPACE" rollout status deployment/kube-state-metrics --timeout="$TIMEOUT"

echo "Monitoring is ready in namespace $NAMESPACE"
echo "Prometheus:   kubectl -n $NAMESPACE port-forward service/prometheus 9090:9090"
echo "Alertmanager: kubectl -n $NAMESPACE port-forward service/alertmanager 9093:9093"
