#!/bin/sh
set -eu

CNPG_VERSION="1.30.0"
CNPG_COMMIT="4b5e244a7d031f67e025c83c1555e7726ecbbfa1"
CNPG_MANIFEST_SHA256="f8bede43fe4ee0d478c2355b204a36876b2ae4faac60f2a9452280b293da3b88"
CNPG_MANIFEST_URL="https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/$CNPG_COMMIT/releases/cnpg-$CNPG_VERSION.yaml"
CNPG_OPERATOR_IMAGE_TAG="ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0"
CNPG_OPERATOR_IMAGE="ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0@sha256:a2701eb97cdd2a34b1fdb2cb51987f544b706e40bec72ae7146cd8580efefebb"
CLIENT="auto"
TIMEOUT="5m"

usage() {
  cat >&2 <<'USAGE'
Usage: install-cnpg.sh [--client auto|k3s|kubectl] [--timeout DURATION]

Downloads the checksum-pinned CloudNativePG 1.30.0 operator manifest,
applies it with server-side apply, and waits for the operator to become ready.
Refuses to replace an existing operator that uses a different image.

Options:
  --client CLIENT     Kubernetes client: auto, k3s, or kubectl (default: auto)
  --timeout DURATION  kubectl wait/rollout timeout (default: 5m)
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
    if ! command -v k3s >/dev/null 2>&1; then
      echo "k3s is required for --client k3s" >&2
      exit 1
    fi
    if [ "$(id -u)" != "0" ]; then
      echo "--client k3s must run as root; rerun with sudo" >&2
      exit 1
    fi
    kubectl_run() {
      k3s kubectl "$@"
    }
    ;;
  kubectl)
    if ! command -v kubectl >/dev/null 2>&1; then
      echo "kubectl is required for --client kubectl" >&2
      exit 1
    fi
    kubectl_run() {
      kubectl "$@"
    }
    ;;
esac

if command -v curl >/dev/null 2>&1; then
  download() {
    curl -fsSL "$1" -o "$2"
  }
elif command -v wget >/dev/null 2>&1; then
  download() {
    wget -qO "$2" "$1"
  }
else
  echo "curl or wget is required" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256_file() {
    sha256sum "$1" | awk '{print $1}'
  }
elif command -v shasum >/dev/null 2>&1; then
  sha256_file() {
    shasum -a 256 "$1" | awk '{print $1}'
  }
else
  echo "sha256sum or shasum is required" >&2
  exit 1
fi

server_git_version=$(
  kubectl_run version -o json | awk '
    /"serverVersion"[[:space:]]*:/ { in_server = 1 }
    in_server && /"gitVersion"[[:space:]]*:/ {
      line = $0
      sub(/^.*"gitVersion"[[:space:]]*:[[:space:]]*"/, "", line)
      sub(/".*$/, "", line)
      print line
      exit
    }
  '
)
server_major=$(printf '%s\n' "$server_git_version" | sed -n 's/^v\([0-9][0-9]*\)\..*/\1/p')
server_minor=$(printf '%s\n' "$server_git_version" | sed -n 's/^v[0-9][0-9]*\.\([0-9][0-9]*\).*/\1/p')

if [ -z "$server_major" ] || [ -z "$server_minor" ]; then
  echo "could not determine the Kubernetes server version" >&2
  exit 1
fi
if [ "$server_major" -ne 1 ] || [ "$server_minor" -lt 34 ] || [ "$server_minor" -gt 36 ]; then
  echo "CloudNativePG $CNPG_VERSION requires Kubernetes 1.34 through 1.36; server is $server_git_version" >&2
  exit 1
fi

existing_operator=""
existing_operator_crd=$(
  kubectl_run get customresourcedefinition/clusters.postgresql.cnpg.io \
    --ignore-not-found -o name
)
if kubectl_run get namespace/cnpg-system >/dev/null 2>&1; then
  existing_operator=$(
    kubectl_run -n cnpg-system get deployment/cnpg-controller-manager \
      --ignore-not-found -o name
  )
  other_operator=$(
    kubectl_run -n cnpg-system get deployments \
      -l app.kubernetes.io/name=cloudnative-pg \
      -o 'jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}'
  )
  if [ -n "$other_operator" ] && [ "$other_operator" != "cnpg-controller-manager" ]; then
    echo "refusing to install alongside another CloudNativePG operator Deployment" >&2
    printf 'found: %s\n' "$other_operator" >&2
    exit 1
  fi
fi
if [ -n "$existing_operator_crd" ] && [ -z "$existing_operator" ]; then
  echo "refusing to install: CloudNativePG Cluster CRD exists without the expected cnpg-controller-manager Deployment" >&2
  exit 1
fi
if [ -n "$existing_operator" ]; then
  existing_operator_image=$(
    kubectl_run -n cnpg-system get deployment/cnpg-controller-manager \
      -o 'jsonpath={.spec.template.spec.containers[?(@.name=="manager")].image}'
  )
  existing_operand_image=$(
    kubectl_run -n cnpg-system get deployment/cnpg-controller-manager \
      -o 'jsonpath={.spec.template.spec.containers[?(@.name=="manager")].env[?(@.name=="OPERATOR_IMAGE_NAME")].value}'
  )
  if [ "$existing_operator_image" != "$CNPG_OPERATOR_IMAGE" ] || [ "$existing_operand_image" != "$CNPG_OPERATOR_IMAGE" ]; then
    echo "refusing to replace existing cnpg-controller-manager image" >&2
    echo "expected: $CNPG_OPERATOR_IMAGE" >&2
    echo "manager:  ${existing_operator_image:-unknown}" >&2
    echo "operand:  ${existing_operand_image:-unknown}" >&2
    exit 1
  fi
  echo "Existing CloudNativePG operator already uses $CNPG_OPERATOR_IMAGE"
fi

WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/deployer-cnpg.XXXXXX")
cleanup() {
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM
MANIFEST="$WORKDIR/cnpg-$CNPG_VERSION.yaml"
PINNED_MANIFEST="$WORKDIR/cnpg-$CNPG_VERSION-pinned.yaml"

echo "Downloading CloudNativePG $CNPG_VERSION from pinned commit $CNPG_COMMIT"
download "$CNPG_MANIFEST_URL" "$MANIFEST"
actual_sha256=$(sha256_file "$MANIFEST")
if [ "$actual_sha256" != "$CNPG_MANIFEST_SHA256" ]; then
  echo "CloudNativePG manifest checksum mismatch" >&2
  echo "expected: $CNPG_MANIFEST_SHA256" >&2
  echo "actual:   $actual_sha256" >&2
  exit 1
fi
echo "Verified CloudNativePG manifest SHA256 $CNPG_MANIFEST_SHA256"

image_references=$(
  awk -v image="$CNPG_OPERATOR_IMAGE_TAG" '
    index($0, image) { count++ }
    END { print count + 0 }
  ' "$MANIFEST"
)
if [ "$image_references" -ne 2 ]; then
  echo "expected exactly 2 CloudNativePG operator image references, found $image_references" >&2
  exit 1
fi
sed "s|$CNPG_OPERATOR_IMAGE_TAG|$CNPG_OPERATOR_IMAGE|g" "$MANIFEST" >"$PINNED_MANIFEST"

kubectl_run apply --server-side --field-manager=deployer-cnpg-installer -f "$PINNED_MANIFEST"
kubectl_run wait --for=condition=Established customresourcedefinition/clusters.postgresql.cnpg.io --timeout="$TIMEOUT"
kubectl_run wait --for=condition=Established customresourcedefinition/failoverquorums.postgresql.cnpg.io --timeout="$TIMEOUT"
kubectl_run -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout="$TIMEOUT"

installed_operator_image=$(
  kubectl_run -n cnpg-system get deployment/cnpg-controller-manager \
    -o 'jsonpath={.spec.template.spec.containers[?(@.name=="manager")].image}'
)
installed_operand_image=$(
  kubectl_run -n cnpg-system get deployment/cnpg-controller-manager \
    -o 'jsonpath={.spec.template.spec.containers[?(@.name=="manager")].env[?(@.name=="OPERATOR_IMAGE_NAME")].value}'
)
if [ "$installed_operator_image" != "$CNPG_OPERATOR_IMAGE" ] || [ "$installed_operand_image" != "$CNPG_OPERATOR_IMAGE" ]; then
  echo "CloudNativePG operator image digest verification failed after rollout" >&2
  exit 1
fi

echo "CloudNativePG $CNPG_VERSION ($CNPG_OPERATOR_IMAGE) is ready on Kubernetes $server_git_version"
