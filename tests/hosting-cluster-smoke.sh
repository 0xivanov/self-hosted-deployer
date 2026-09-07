#!/bin/sh
set -eu

IMAGE='rancher/k3s@sha256:2074403abe1bded11ef3dde09d457e13be8e0b64c218b1c4f8269b4565cfbc65'
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/hosting-cluster-smoke.XXXXXX")

NAME="codex-$(basename "$WORK")"
created=0
cleanup() {
  set +e
  if [ "$created" = 1 ]; then docker rm -f "$NAME" >/dev/null 2>&1; fi
  rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  docker pull "$IMAGE" >/dev/null
fi

# Generate the policy with the actual renderer from an isolated source copy.
mkdir -p "$WORK/src"
(cd "$ROOT" && git ls-files -z --cached --others --exclude-standard -- internal go.mod go.sum) >"$WORK/sources"
tar -C "$ROOT" --null -T "$WORK/sources" -cf - | tar -C "$WORK/src" -xf -
cat >"$WORK/src/internal/ingress/hosting_smoke_generate_test.go" <<'EOF'
package ingress

import (
    "os"
    "testing"
    "sigs.k8s.io/yaml"
    "github.com/0xivanov/self-hosted-deployer/internal/appconfig"
)

func TestEmitSmokeNetworkPolicy(t *testing.T) {
    cfg := appconfig.Config{
        Name: "hosted-api",
        Image: "busybox:1.36",
        Service: appconfig.ServiceConfig{Port: 8080, Health: appconfig.HealthConfig{Path: "/"}},
        Deploy: appconfig.DeployConfig{Replicas: 1},
        Placement: appconfig.PlacementConfig{Arch: appconfig.PlacementArchAny},
        Hosting: &appconfig.HostingConfig{
            Version: "v1", MaxReplicas: 1,
            Resources: appconfig.HostingResources{
                Requests: appconfig.ResourceQuantities{CPU: "10m", Memory: "16Mi", EphemeralStorage: "16Mi"},
                Limits: appconfig.ResourceQuantities{CPU: "100m", Memory: "64Mi", EphemeralStorage: "64Mi"},
            },
            Network: appconfig.HostingNetwork{Egress: []appconfig.HostingEgressRule{{CIDR: "10.42.0.0/16", Ports: []int{8080}}}},
        },
    }
    policy, err := networkPolicyForHostedApp(cfg, "hosted")
    if err != nil { t.Fatal(err) }
    output := os.Getenv("SMOKE_POLICY_OUTPUT")
    if output == "" { t.Fatal("SMOKE_POLICY_OUTPUT is required") }
    data, err := yaml.Marshal(policy)
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(output, data, 0600); err != nil { t.Fatal(err) }
}
EOF
(cd "$WORK/src" && SMOKE_POLICY_OUTPUT="$WORK/policy.yaml" go test ./internal/ingress -run TestEmitSmokeNetworkPolicy -count=1 >/dev/null)
[ -s "$WORK/policy.yaml" ] || { echo 'renderer did not emit a NetworkPolicy' >&2; exit 1; }
rm -f "$WORK/src/internal/ingress/hosting_smoke_generate_test.go"

docker run --privileged --name "$NAME" -d "$IMAGE" server --disable traefik --disable servicelb >/dev/null
created=1
for _ in $(seq 1 45); do
  if docker exec "$NAME" kubectl get node --no-headers 2>/dev/null | grep -q ' Ready '; then break; fi
  sleep 2
done
docker exec "$NAME" kubectl get node --no-headers | grep -q ' Ready ' || { echo 'k3s did not become ready' >&2; exit 1; }

docker exec -i "$NAME" sh -c 'cat >/tmp/smoke.yaml' <<'EOF'
apiVersion: v1
kind: Namespace
metadata: {name: hosted}
---
apiVersion: v1
kind: Namespace
metadata: {name: deployer-monitoring}
---
apiVersion: v1
kind: Namespace
metadata: {name: other}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: default, namespace: hosted}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: default, namespace: deployer-monitoring}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: default, namespace: other}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: hosted-content, namespace: hosted}
data:
  index.html: "ok"
---
apiVersion: v1
kind: Pod
metadata:
  name: hosted-api
  namespace: hosted
  labels: {app.kubernetes.io/name: hosted-api, deployer.io/app: hosted-api, deployer.io/hosting-profile: v1}
spec:
  containers:
  - name: app
    image: busybox:1.36
    command: ["sh", "-c", "httpd -f -p 8080 -h /www & httpd -f -p 8081 -h /www & sleep 3600"]
    volumeMounts: [{name: content, mountPath: /www}]
  volumes: [{name: content, configMap: {name: hosted-content}}]
---
apiVersion: v1
kind: Service
metadata: {name: hosted-api, namespace: hosted}
spec:
  selector: {app.kubernetes.io/name: hosted-api}
  ports: [{name: http, port: 8080, targetPort: 8080}, {name: alternate, port: 8081, targetPort: 8081}]
---
apiVersion: v1
kind: Pod
metadata: {name: traefik-probe, namespace: kube-system, labels: {app.kubernetes.io/name: traefik}}
spec:
  containers: [{name: probe, image: busybox:1.36, command: ["sleep", "3600"]}]
---
apiVersion: v1
kind: Pod
metadata: {name: monitor-probe, namespace: deployer-monitoring}
spec:
  containers: [{name: probe, image: busybox:1.36, command: ["sleep", "3600"]}]
---
apiVersion: v1
kind: Pod
metadata: {name: other-probe, namespace: other}
spec:
  containers:
  - name: probe
    image: busybox:1.36
    command: ["sh", "-c", "mkdir /tmp/www; echo ok >/tmp/www/index.html; httpd -f -p 8080 -h /tmp/www & httpd -f -p 8081 -h /tmp/www & sleep 3600"]
EOF
docker cp "$WORK/policy.yaml" "$NAME:/tmp/policy.yaml"
docker exec "$NAME" kubectl apply -f /tmp/smoke.yaml >/dev/null
for pair in hosted/hosted-api other/other-probe kube-system/traefik-probe deployer-monitoring/monitor-probe; do
  docker exec "$NAME" kubectl wait --for=condition=Ready pod/"${pair#*/}" -n "${pair%/*}" --timeout=120s >/dev/null
done
hosted_ip=$(docker exec "$NAME" kubectl get pod -n hosted hosted-api -o jsonpath='{.status.podIP}')
other_ip=$(docker exec "$NAME" kubectl get pod -n other other-probe -o jsonpath='{.status.podIP}')

probe() {
  docker exec "$NAME" kubectl exec -n "$1" "$2" -- wget -T 3 -qO- "http://$3:$4"
}
assert_200() {
  value=$(probe "$1" "$2" "$3" "$4")
  [ "$value" = ok ] || { echo "expected HTTP 200 from $1/$2 to $3:$4, got $value" >&2; exit 1; }
}
assert_denied() {
  # Baseline and recovery prove the listener works. Require a network denial,
  # never a DNS, kubectl, or HTTP failure. k3s may actively reject
  # connections instead of dropping packets. Baseline/recovery cover listeners.
  if probe "$1" "$2" "$3" "$4" >"$WORK/denied.out" 2>"$WORK/denied.err"; then
    echo "unexpected network access from $1/$2 to $3:$4" >&2; exit 1
  fi
  grep -Eq 'timed out|No route to host|Host is unreachable|Connection refused' "$WORK/denied.err" || {
    cat "$WORK/denied.err" >&2; echo 'unexpected probe failure' >&2; exit 1;
  }
}

assert_200 other other-probe "$hosted_ip" 8080
assert_200 other other-probe "$hosted_ip" 8081
assert_200 kube-system traefik-probe "$hosted_ip" 8081
assert_200 hosted hosted-api "$other_ip" 8080
assert_200 hosted hosted-api "$other_ip" 8081
docker exec "$NAME" kubectl apply -f /tmp/policy.yaml >/dev/null
# k3s network policy reconciliation is asynchronous.
sleep 5
assert_200 kube-system traefik-probe "$hosted_ip" 8080
assert_200 deployer-monitoring monitor-probe "$hosted_ip" 8080
assert_200 hosted hosted-api "$other_ip" 8080
docker exec "$NAME" kubectl exec -n hosted hosted-api -- nslookup kubernetes.default.svc.cluster.local >/dev/null
assert_denied other other-probe "$hosted_ip" 8080
assert_denied other other-probe "$hosted_ip" 8081
assert_denied kube-system traefik-probe "$hosted_ip" 8081
assert_denied hosted hosted-api "$other_ip" 8081
docker exec "$NAME" kubectl delete -f /tmp/policy.yaml >/dev/null
sleep 5
assert_200 other other-probe "$hosted_ip" 8080
assert_200 other other-probe "$hosted_ip" 8081
assert_200 kube-system traefik-probe "$hosted_ip" 8081
assert_200 hosted hosted-api "$other_ip" 8081
echo "hosting cluster smoke passed ($IMAGE)"
