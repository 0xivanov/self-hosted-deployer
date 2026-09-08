#!/usr/bin/env bash
# Run only inside the prepared, synthetic Mac qualification VM.
set -euo pipefail
[[ $(id -u) == 0 && $(hostname) == lima-deployer-poc-lab ]] || {
  echo 'This test requires root inside lima-deployer-poc-lab.' >&2; exit 2;
}
[[ ${1:-} == --apply && $# == 1 ]] || { echo 'Usage: mac-app-lifecycle-smoke.sh --apply' >&2; exit 2; }
config=/root/lab-cli.json
namespace=hosting-mac-lab-a
app=hosting-lab-app
fixture=/tmp/hosting-lab-app.yaml
[[ -f $config && -f $fixture ]] || { echo 'Prepared lab context and app fixture are required.' >&2; exit 2; }
# Require private credentials without printing their contents.
python3 - "$config" <<'PY'
import json,sys
from pathlib import Path
p=Path(sys.argv[1])
assert p.stat().st_mode & 0o077 == 0, 'Lab credential file must be private'
c=json.loads(p.read_text())['contexts']['mac-lab-a']
assert c['server_url']=='https://deployer-poc.test:7443' and c['environment_id']=='mac-lab-a'
assert c['server_identity'], 'Lab context must be bound to its server identity'
PY
cli=(deployer --config "$config" --context mac-lab-a)
kubectl=(k3s kubectl -n "$namespace")
work=$(mktemp -d /tmp/hosting-lifecycle.XXXXXX)
needs_recovery=0
cleanup() {
  if [[ $needs_recovery == 1 ]]; then
    "${cli[@]}" deploy -f "$fixture" >/dev/null || echo 'Restore the healthy lab fixture before continuing.' >&2
  fi
  rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
"${cli[@]}" preflight -f "$fixture" >/dev/null
"${cli[@]}" deploy -f "$fixture" >/dev/null
"${kubectl[@]}" rollout status "deployment/$app" --timeout=120s
"${kubectl[@]}" get pods -l "app.kubernetes.io/name=$app" -o json > "$work/baseline.json"
probe() { curl -fsS --max-time 5 -H 'Host: app.deployer-poc.test' http://10.81.0.1/ | grep -qx hosting-lab-ok; }
probe
sed 's|path: /|path: /missing-readiness|' "$fixture" > "$work/bad.yaml"
needs_recovery=1
"${cli[@]}" deploy -f "$work/bad.yaml" >/dev/null
observed=0
for _ in $(seq 1 45); do
  "${kubectl[@]}" get events --field-selector reason=Unhealthy -o json > "$work/events.json"
  "${kubectl[@]}" get pods -l "app.kubernetes.io/name=$app" -o json > "$work/current.json"
  if python3 - "$work" 2>"$work/readiness-check.log" <<'PY'
import json,sys
from pathlib import Path
p=Path(sys.argv[1]); old=json.loads((p/'baseline.json').read_text())['items']; pods=json.loads((p/'current.json').read_text())['items']
old_ids={x['metadata']['uid'] for x in old}
new={x['metadata']['uid'] for x in pods if x['metadata']['uid'] not in old_ids}
events=json.loads((p/'events.json').read_text())['items']
assert any(e.get('involvedObject',{}).get('uid') in new and 'Readiness probe failed' in e.get('message','') for e in events)
for original in old:
    if not any(c['type']=='Ready' and c['status']=='True' for c in original['status'].get('conditions',[])): continue
    current=next(x for x in pods if x['metadata']['uid']==original['metadata']['uid'])
    assert any(c['type']=='Ready' and c['status']=='True' for c in current['status']['conditions'])
    assert [c['restartCount'] for c in current['status']['containerStatuses']]==[c['restartCount'] for c in original['status']['containerStatuses']]
    break
else: raise AssertionError('No healthy baseline pod')
PY
  then observed=1; break; fi
  sleep 2
done
[[ $observed == 1 ]] || { echo 'Did not verify bad readiness with preserved healthy replica.' >&2; exit 1; }
probe
"${cli[@]}" deploy -f "$fixture" >/dev/null
"${kubectl[@]}" rollout status "deployment/$app" --timeout=120s
probe
needs_recovery=0
echo 'PASS: hosted app ingress, failed readiness containment, and explicit rollback.'
