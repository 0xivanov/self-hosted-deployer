#!/bin/sh
set -eu

CLIENT="auto"
TIMEOUT="5m"
ALERTMANAGER_CONFIG=""
GRAFANA_DOMAIN=""
GRAFANA_TLS_ISSUER="deployer-letsencrypt"
GRAFANA_ADMIN_USER="admin"
GRAFANA_ADMIN_PASSWORD_FILE=""
NAMESPACE="deployer-monitoring"
SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
MONITORING_DIR="$SCRIPT_DIR/../deploy/monitoring"
STACK_MANIFEST="$MONITORING_DIR/stack.yaml"
OBSERVABILITY_MANIFEST="$MONITORING_DIR/observability.yaml"
LOKI_CONFIG="$MONITORING_DIR/loki.yml"
ALLOY_CONFIG="$MONITORING_DIR/alloy.config.alloy"
GRAFANA_DATASOURCES="$MONITORING_DIR/grafana/datasources.yml"
GRAFANA_DASHBOARD_PROVIDER="$MONITORING_DIR/grafana/dashboard-provider.yml"
GRAFANA_DASHBOARDS="$MONITORING_DIR/grafana/dashboards"

usage() {
  cat >&2 <<'USAGE'
Usage: install-monitoring.sh [OPTIONS]

Installs the deployer's Prometheus, Alertmanager, Loki, Alloy, Grafana, and
kube-state-metrics stack. Storage and services are pinned to the k3s
control-plane node. Only Grafana is exposed when --grafana-domain is set.

Options:
  --client CLIENT                  Kubernetes client: auto, k3s, or kubectl (default: auto)
  --timeout DURATION               Rollout timeout (default: 5m)
  --alertmanager-config FILE       Alertmanager YAML to store as a Kubernetes Secret
  --grafana-domain DOMAIN          Public HTTPS hostname for Grafana; omit for private-only access
  --grafana-tls-issuer NAME        cert-manager ClusterIssuer (default: deployer-letsencrypt)
  --grafana-admin-user USER        Initial Grafana administrator (default: admin)
  --grafana-admin-password-file FILE
                                    Initial administrator password; generated when omitted
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
    --grafana-domain)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--grafana-domain requires a hostname" >&2
        usage
        exit 2
      fi
      GRAFANA_DOMAIN="$2"
      shift 2
      ;;
    --grafana-tls-issuer)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--grafana-tls-issuer requires a ClusterIssuer name" >&2
        usage
        exit 2
      fi
      GRAFANA_TLS_ISSUER="$2"
      shift 2
      ;;
    --grafana-admin-user)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--grafana-admin-user requires a username" >&2
        usage
        exit 2
      fi
      GRAFANA_ADMIN_USER="$2"
      shift 2
      ;;
    --grafana-admin-password-file)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "--grafana-admin-password-file requires a file" >&2
        usage
        exit 2
      fi
      GRAFANA_ADMIN_PASSWORD_FILE="$2"
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

for required_file in \
  "$STACK_MANIFEST" \
  "$OBSERVABILITY_MANIFEST" \
  "$LOKI_CONFIG" \
  "$ALLOY_CONFIG" \
  "$GRAFANA_DATASOURCES" \
  "$GRAFANA_DASHBOARD_PROVIDER"; do
  if [ ! -f "$required_file" ]; then
    echo "monitoring asset not found: $required_file" >&2
    exit 1
  fi
done
if [ ! -d "$GRAFANA_DASHBOARDS" ]; then
  echo "Grafana dashboard directory not found: $GRAFANA_DASHBOARDS" >&2
  exit 1
fi
if [ -n "$ALERTMANAGER_CONFIG" ] && [ ! -s "$ALERTMANAGER_CONFIG" ]; then
  echo "Alertmanager config is missing or empty: $ALERTMANAGER_CONFIG" >&2
  exit 1
fi
if [ -n "$GRAFANA_ADMIN_PASSWORD_FILE" ] && [ ! -s "$GRAFANA_ADMIN_PASSWORD_FILE" ]; then
  echo "Grafana password file is missing or empty: $GRAFANA_ADMIN_PASSWORD_FILE" >&2
  exit 1
fi

case "$GRAFANA_DOMAIN" in
  "")
    ;;
  .*|*.|*..*|*[!A-Za-z0-9.-]*)
    echo "invalid Grafana domain: $GRAFANA_DOMAIN" >&2
    exit 2
    ;;
esac
case "$GRAFANA_ADMIN_USER" in
  *[!A-Za-z0-9._-]*)
    echo "invalid Grafana administrator username: $GRAFANA_ADMIN_USER" >&2
    exit 2
    ;;
esac
case "$GRAFANA_TLS_ISSUER" in
  *[!A-Za-z0-9.-]*)
    echo "invalid Grafana TLS issuer: $GRAFANA_TLS_ISSUER" >&2
    exit 2
    ;;
esac

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

grafana_secret_exists="0"
if kubectl_run -n "$NAMESPACE" get secret grafana-admin >/dev/null 2>&1; then
  grafana_secret_exists="1"
fi

if [ "$grafana_secret_exists" = "1" ] && [ -n "$GRAFANA_ADMIN_PASSWORD_FILE" ]; then
  echo "Grafana is already initialized; refusing to replace its password through the bootstrap secret" >&2
  echo "Reset the administrator password from the running Grafana instance instead" >&2
  exit 1
fi

if [ "$grafana_secret_exists" = "0" ]; then
  grafana_user_file="$WORKDIR/grafana-admin-user"
  grafana_password_file="$WORKDIR/grafana-admin-password"
  grafana_secret_key_file="$WORKDIR/grafana-secret-key"

  printf '%s' "$GRAFANA_ADMIN_USER" >"$grafana_user_file"
  if [ -n "$GRAFANA_ADMIN_PASSWORD_FILE" ]; then
    tr -d '\r\n' <"$GRAFANA_ADMIN_PASSWORD_FILE" >"$grafana_password_file"
  else
    od -An -N24 -tx1 /dev/urandom | tr -d ' \n' >"$grafana_password_file"
  fi
  password_length=$(wc -c <"$grafana_password_file" | tr -d ' ')
  if [ "$password_length" -lt 16 ] || [ "$password_length" -gt 128 ]; then
    echo "Grafana administrator password must be between 16 and 128 characters" >&2
    exit 2
  fi
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n' >"$grafana_secret_key_file"

  kubectl_run -n "$NAMESPACE" create secret generic grafana-admin \
    --from-file="admin-user=$grafana_user_file" \
    --from-file="admin-password=$grafana_password_file" \
    --from-file="secret-key=$grafana_secret_key_file" \
    --dry-run=client -o yaml | kubectl_run apply -f -
fi

if [ -n "$GRAFANA_DOMAIN" ]; then
  grafana_runtime_domain="$GRAFANA_DOMAIN"
  grafana_root_url="https://$GRAFANA_DOMAIN"
else
  grafana_runtime_domain="localhost"
  grafana_root_url="http://localhost:3000"
fi

kubectl_run -n "$NAMESPACE" create configmap loki-config \
  --from-file="loki.yml=$LOKI_CONFIG" \
  --dry-run=client -o yaml | kubectl_run apply -f -
kubectl_run -n "$NAMESPACE" create configmap alloy-config \
  --from-file="config.alloy=$ALLOY_CONFIG" \
  --dry-run=client -o yaml | kubectl_run apply -f -
kubectl_run -n "$NAMESPACE" create configmap grafana-provisioning \
  --from-file="datasources.yml=$GRAFANA_DATASOURCES" \
  --from-file="dashboard-provider.yml=$GRAFANA_DASHBOARD_PROVIDER" \
  --dry-run=client -o yaml | kubectl_run apply -f -
kubectl_run -n "$NAMESPACE" create configmap grafana-dashboards \
  --from-file="$GRAFANA_DASHBOARDS" \
  --dry-run=client -o yaml | kubectl_run apply -f -
kubectl_run -n "$NAMESPACE" create configmap grafana-runtime \
  --from-literal="domain=$grafana_runtime_domain" \
  --from-literal="root-url=$grafana_root_url" \
  --dry-run=client -o yaml | kubectl_run apply -f -

kubectl_run apply --server-side --field-manager=deployer-monitoring-installer -f "$OBSERVABILITY_MANIFEST"
kubectl_run -n "$NAMESPACE" rollout restart deployment/loki deployment/alloy deployment/grafana
kubectl_run -n "$NAMESPACE" rollout status deployment/loki --timeout="$TIMEOUT"
kubectl_run -n "$NAMESPACE" rollout status deployment/alloy --timeout="$TIMEOUT"
kubectl_run -n "$NAMESPACE" rollout status deployment/grafana --timeout="$TIMEOUT"

if [ -n "$GRAFANA_DOMAIN" ]; then
  kubectl_run apply -f - <<EOF
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: grafana-security-headers
  namespace: $NAMESPACE
  labels:
    app.kubernetes.io/name: grafana
    app.kubernetes.io/managed-by: deployer
spec:
  headers:
    contentTypeNosniff: true
    customFrameOptionsValue: DENY
    referrerPolicy: same-origin
    stsSeconds: 31536000
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: grafana
  namespace: $NAMESPACE
  labels:
    app.kubernetes.io/name: grafana
    app.kubernetes.io/managed-by: deployer
  annotations:
    cert-manager.io/cluster-issuer: $GRAFANA_TLS_ISSUER
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.middlewares: $NAMESPACE-grafana-security-headers@kubernetescrd
    traefik.ingress.kubernetes.io/router.tls: "true"
spec:
  ingressClassName: traefik
  rules:
    - host: $GRAFANA_DOMAIN
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: grafana
                port:
                  number: 3000
  tls:
    - hosts:
        - $GRAFANA_DOMAIN
      secretName: grafana-tls
EOF
fi

kubectl_run apply --server-side --field-manager=deployer-monitoring-installer -f "$STACK_MANIFEST"
kubectl_run -n "$NAMESPACE" rollout restart deployment/prometheus deployment/alertmanager
kubectl_run -n "$NAMESPACE" rollout status deployment/prometheus --timeout="$TIMEOUT"
kubectl_run -n "$NAMESPACE" rollout status deployment/alertmanager --timeout="$TIMEOUT"
kubectl_run -n "$NAMESPACE" rollout status deployment/kube-state-metrics --timeout="$TIMEOUT"

echo "Monitoring is ready in namespace $NAMESPACE"
echo "Prometheus:   kubectl -n $NAMESPACE port-forward service/prometheus 9090:9090"
echo "Alertmanager: kubectl -n $NAMESPACE port-forward service/alertmanager 9093:9093"
echo "Grafana:      kubectl -n $NAMESPACE port-forward service/grafana 3000:3000"
if [ -n "$GRAFANA_DOMAIN" ]; then
  echo "Grafana URL:  https://$GRAFANA_DOMAIN"
fi
if [ "$grafana_secret_exists" = "0" ] && [ -z "$GRAFANA_ADMIN_PASSWORD_FILE" ]; then
  echo "Grafana generated an administrator password in secret $NAMESPACE/grafana-admin"
fi
