#!/bin/sh
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/hosting-dashboard-check.XXXXXX")
trap 'rm -rf "$WORK"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
python3 - "$ROOT" "$WORK" <<'PY'
import json
import pathlib
import sys
root, work = map(pathlib.Path, sys.argv[1:])
dashboard = json.loads((root / 'deploy/monitoring/grafana/dashboards/hosting-overview.json').read_text())
assert dashboard['uid'] == 'hosting-overview'
ids = [panel['id'] for panel in dashboard['panels']]
assert len(ids) == len(set(ids)), 'duplicate panel ids'
rules = []
for panel in dashboard['panels']:
    for index, target in enumerate(panel.get('targets', [])):
        expr = target['expr'].replace('$namespace', 'hosting-test').replace('$app', 'example')
        assert '$' not in expr, 'unhandled Grafana variable in query'
        rules.append({'record': f"hosting_dashboard_test_{panel['id']}_{index}", 'expr': expr})
assert rules, 'dashboard has no queries'
(work / 'rules.yml').write_text(json.dumps({'groups': [{'name': 'dashboard-query-check', 'rules': rules}]}))
PY
docker run --rm --network none --user "$(id -u):$(id -g)" --entrypoint /bin/promtool \
  -v "$WORK:/checks:ro" prom/prometheus:v3.13.0 check rules /checks/rules.yml
