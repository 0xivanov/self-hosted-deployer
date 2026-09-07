#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
script="$repo_dir/scripts/create-hosting-namespace.sh"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

assert_fail() {
  if "$@" >"$tmp_dir/stdout" 2>"$tmp_dir/stderr"; then
    echo "expected command to fail: $*" >&2
    exit 1
  fi
}

preview=$($script --namespace hosting-test --cpu 500m --memory 512Mi --ephemeral-storage 1Gi --pods 10)
grep -Fq 'Preview only; no Kubernetes changes made.' <<<"$preview"
grep -Fq 'kind: ResourceQuota' <<<"$preview"
grep -Fq 'kind: LimitRange' <<<"$preview"

assert_fail "$script" --namespace deployer-apps --cpu 500m --memory 512Mi --ephemeral-storage 1Gi --pods 10
assert_fail "$script" --namespace hosting-test --cpu '500m;touch' --memory 512Mi --ephemeral-storage 1Gi --pods 10
assert_fail "$script" --namespace hosting-test --cpu 500m --memory 512Mi --ephemeral-storage 1Gi --pods 0
assert_fail "$script" --apply --namespace hosting-test --cpu 500m --memory 512Mi --ephemeral-storage 1Gi --pods 10

cat >"$tmp_dir/kubectl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${MOCK_KUBECTL_LOG:?}"
[[ "$1" == --kubeconfig && "$3" == --context ]]
[[ "$2" == "${MOCK_KUBECONFIG:?}" && "$4" == "customer a" ]]
shift 4
if [[ "${1:-}" == "get" ]]; then
  case "${MOCK_MODE:-}" in existing) echo namespace/hosting-test ;; api-error) exit 1 ;; esac
  exit 0
fi
if [[ "${1:-}" == "create" && "${MOCK_MODE:-}" == create-error ]]; then exit 1; fi
if [[ "${1:-}" == "apply" ]]; then
  cat >/dev/null
fi
EOF
chmod +x "$tmp_dir/kubectl"
touch "$tmp_dir/kube config"
export MOCK_KUBECONFIG="$tmp_dir/kube config"
export MOCK_KUBECTL_LOG="$tmp_dir/kubectl.log"
PATH="$tmp_dir:$PATH" "$script" --apply --kubeconfig "$tmp_dir/kube config" --context "customer a" \
  --namespace hosting-test --cpu 500m --memory 512Mi --ephemeral-storage 1Gi --pods 10 >"$tmp_dir/apply.out"
grep -Fq 'create namespace hosting-test' "$MOCK_KUBECTL_LOG"
grep -Fq 'apply -f -' "$MOCK_KUBECTL_LOG"
grep -Fq 'Created namespace and applied hosting budgets' "$tmp_dir/apply.out"

for mode in existing api-error create-error; do
  : >"$MOCK_KUBECTL_LOG"
  export MOCK_MODE=$mode
  assert_fail env PATH="$tmp_dir:$PATH" "$script" --apply --kubeconfig "$MOCK_KUBECONFIG" --context "customer a" \
    --namespace hosting-test --cpu 500m --memory 512Mi --ephemeral-storage 1Gi --pods 10
  if grep -Fq 'apply -f -' "$MOCK_KUBECTL_LOG"; then echo 'unexpected quota mutation' >&2; exit 1; fi
  if [[ "$mode" != create-error ]] && grep -Fq 'create namespace' "$MOCK_KUBECTL_LOG"; then echo 'unexpected namespace mutation' >&2; exit 1; fi
done
for option in cpu memory ephemeral-storage pods; do
  value=18446744073709551617
  case "$option" in memory|ephemeral-storage) value=${value}Gi ;; esac
  assert_fail "$script" --namespace hosting-test --cpu 500m --memory 512Mi --ephemeral-storage 1Gi --pods 10 "--$option" "$value"
done
assert_fail "$script" --namespace "hosting-$(printf '%060d' 1)" --cpu 500m --memory 512Mi --ephemeral-storage 1Gi --pods 10

echo 'hosting namespace script tests passed' 
