#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: create-hosting-namespace.sh --namespace hosting-NAME --cpu QUANTITY --memory QUANTITY --ephemeral-storage QUANTITY --pods COUNT [options]
  --apply                 Create the namespace and apply quota manifests.
  --kubeconfig PATH       Required with --apply; never inferred.
  --context NAME          Required with --apply; never inferred.
EOF
}

namespace=''; cpu=''; memory=''; ephemeral_storage=''; pods=''; apply=false; kubeconfig=''; kube_context=''
while (($#)); do
  case "$1" in
    --namespace) [[ $# -ge 2 ]] || { usage; exit 2; }; namespace=$2; shift 2 ;;
    --cpu) [[ $# -ge 2 ]] || { usage; exit 2; }; cpu=$2; shift 2 ;;
    --memory) [[ $# -ge 2 ]] || { usage; exit 2; }; memory=$2; shift 2 ;;
    --ephemeral-storage) [[ $# -ge 2 ]] || { usage; exit 2; }; ephemeral_storage=$2; shift 2 ;;
    --pods) [[ $# -ge 2 ]] || { usage; exit 2; }; pods=$2; shift 2 ;;
    --apply) apply=true; shift ;;
    --kubeconfig) [[ $# -ge 2 ]] || { usage; exit 2; }; kubeconfig=$2; shift 2 ;;
    --context) [[ $# -ge 2 ]] || { usage; exit 2; }; kube_context=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

fail() { echo "error: $*" >&2; exit 2; }
[[ "$namespace" =~ ^hosting-[a-z0-9]([a-z0-9-]{0,60}[a-z0-9])?$ ]] || fail "namespace must match hosting-* with lowercase letters, digits, and hyphens"
((${#namespace} <= 63)) || fail "namespace must not exceed 63 characters"
case "$namespace" in deployer-apps|kube-system|default|hosting-) fail "namespace is reserved or invalid" ;; esac

positive_integer() { [[ "$1" =~ ^[1-9][0-9]*$ ]] && ((${#1} <= 10)); }
validate_cpu() {
  ((${#1} <= 6)) || return 1
  if [[ "$1" =~ ^([1-9][0-9]*)m$ ]]; then ((10#${BASH_REMATCH[1]} <= 64000))
  elif [[ "$1" =~ ^([1-9][0-9]*)$ ]]; then ((10#${BASH_REMATCH[1]} <= 64))
  else return 1; fi
}
validate_memory() {
  [[ "$1" =~ ^([1-9][0-9]*)(Ki|Mi|Gi|Ti)$ ]] || return 1
  local value=${BASH_REMATCH[1]} unit=${BASH_REMATCH[2]} maximum
  ((${#value} <= 10)) || return 1
  case "$unit" in Ki) maximum=1073741824 ;; Mi) maximum=1048576 ;; Gi) maximum=1024 ;; Ti) maximum=1 ;; esac
  ((10#$value <= maximum))
}
validate_cpu "$cpu" || fail "--cpu must be a positive quantity no greater than 64000m"
validate_memory "$memory" || fail "--memory must be a positive Ki/Mi/Gi/Ti quantity no greater than 1Ti"
validate_memory "$ephemeral_storage" || fail "--ephemeral-storage must be a positive Ki/Mi/Gi/Ti quantity no greater than 1Ti"
positive_integer "$pods" || fail "--pods must be a positive integer"
((10#$pods <= 10000)) || fail "--pods must not exceed 10000"
if $apply; then
  [[ -n "$kubeconfig" && -n "$kube_context" ]] || fail "--apply requires explicit --kubeconfig and --context"
  [[ -f "$kubeconfig" ]] || fail "kubeconfig does not exist: $kubeconfig"
fi

render_manifest() {
  cat <<EOF
apiVersion: v1
kind: ResourceQuota
metadata:
  name: hosting-budget
  namespace: ${namespace}
spec:
  hard:
    requests.cpu: ${cpu}
    limits.cpu: ${cpu}
    requests.memory: ${memory}
    limits.memory: ${memory}
    requests.ephemeral-storage: ${ephemeral_storage}
    limits.ephemeral-storage: ${ephemeral_storage}
    pods: "${pods}"
---
apiVersion: v1
kind: LimitRange
metadata:
  name: hosting-container-budget
  namespace: ${namespace}
spec:
  limits:
  - type: Container
    max:
      cpu: ${cpu}
      memory: ${memory}
      ephemeral-storage: ${ephemeral_storage}
EOF
}

if ! $apply; then
  echo "# Preview only; no Kubernetes changes made."
  render_manifest
  exit 0
fi
kubectl_cmd=(kubectl --kubeconfig "$kubeconfig" --context "$kube_context")
if existing="$("${kubectl_cmd[@]}" get namespace "$namespace" -o name --ignore-not-found)"; then
  [[ -z "$existing" ]] || fail "namespace already exists: $namespace"
else
  fail "unable to verify namespace existence"
fi
"${kubectl_cmd[@]}" create namespace "$namespace"
render_manifest | "${kubectl_cmd[@]}" apply -f -
echo "Created namespace and applied hosting budgets: $namespace"
