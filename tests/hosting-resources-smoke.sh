#!/usr/bin/env bash
set -euo pipefail
K3S_IMAGE='rancher/k3s@sha256:2074403abe1bded11ef3dde09d457e13be8e0b64c218b1c4f8269b4565cfbc65'
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/hosting-resources-smoke.XXXXXX")
NAME="codex-$(basename "$WORK")"; APP_IMAGE="hosting-resource-smoke-app:$(basename "$WORK")"; ROOT_IMAGE="hosting-resource-smoke-root:$(basename "$WORK")"; created=0
cleanup() { set +e; [[ "$created" = 1 ]] && docker rm -f "$NAME" >/dev/null 2>&1; docker image rm "$ROOT_IMAGE" "$APP_IMAGE" >/dev/null 2>&1; rm -rf "$WORK"; }
trap cleanup EXIT; trap 'exit 130' INT; trap 'exit 143' TERM
command -v docker >/dev/null || { echo 'docker is required' >&2; exit 2; }; docker info >/dev/null 2>&1 || { echo 'Docker daemon is unavailable' >&2; exit 2; }
if ! docker image inspect "$K3S_IMAGE" >/dev/null 2>&1; then docker pull "$K3S_IMAGE" >/dev/null; fi
mkdir -p "$WORK/image"
cat >"$WORK/image/Dockerfile" <<'EOF2'
FROM busybox:1.36
RUN mkdir -p /www && printf 'ok\n' >/www/index.html
USER 65532
CMD ["httpd", "-f", "-p", "8080", "-h", "/www"]
EOF2
docker build --pull=false -t "$APP_IMAGE" "$WORK/image" >/dev/null
cat >"$WORK/image/Dockerfile.root" <<EOF2
FROM $APP_IMAGE
USER 0
EOF2
docker build --pull=false -f "$WORK/image/Dockerfile.root" -t "$ROOT_IMAGE" "$WORK/image" >/dev/null
mkdir -p "$WORK/src"; (cd "$ROOT" && git ls-files -z --cached --others --exclude-standard -- internal go.mod go.sum) >"$WORK/sources"; tar -C "$ROOT" --null -T "$WORK/sources" -cf - | tar -C "$WORK/src" -xf -
cat >"$WORK/src/internal/ingress/hosting_resources_smoke_generate_test.go" <<'EOF2'
package ingress
import ( "fmt"; "os"; "testing"; "github.com/0xivanov/self-hosted-deployer/internal/appconfig"; "k8s.io/apimachinery/pkg/apis/meta/v1"; "sigs.k8s.io/yaml" )
func TestEmitHostingResourceDeployments(t *testing.T) {
 o, ai, ri := os.Getenv("SMOKE_DEPLOYMENT_OUTPUT"), os.Getenv("SMOKE_APP_IMAGE"), os.Getenv("SMOKE_ROOT_IMAGE"); if o==""||ai==""||ri=="" { t.Fatal("smoke variables required") }
 base:=func(n,i string) appconfig.Config { return appconfig.Config{Name:n,Image:i,Service:appconfig.ServiceConfig{Port:8080,Health:appconfig.HealthConfig{Path:"/"}},Deploy:appconfig.DeployConfig{Replicas:1},Placement:appconfig.PlacementConfig{Arch:appconfig.PlacementArchAny},Hosting:&appconfig.HostingConfig{Version:"v1",MaxReplicas:1,Resources:appconfig.HostingResources{Requests:appconfig.ResourceQuantities{CPU:"25m",Memory:"16Mi",EphemeralStorage:"16Mi"},Limits:appconfig.ResourceQuantities{CPU:"50m",Memory:"32Mi",EphemeralStorage:"32Mi"}}}} }
 a,e:=deploymentForApp(base("hosted-api",ai),"hosting-smoke",""); if e!=nil { t.Fatal(e) }; r,e:=deploymentForApp(base("root-api",ri),"hosting-smoke",""); if e!=nil { t.Fatal(e) }; a.TypeMeta=v1.TypeMeta{APIVersion:"apps/v1",Kind:"Deployment"}; r.TypeMeta=v1.TypeMeta{APIVersion:"apps/v1",Kind:"Deployment"}; ad,e:=yaml.Marshal(a); if e!=nil { t.Fatal(e) }; rd,e:=yaml.Marshal(r); if e!=nil { t.Fatal(e) }; if e=os.WriteFile(o,[]byte(fmt.Sprintf("%s---\n%s",ad,rd)),0600); e!=nil { t.Fatal(e) }
}
EOF2
(cd "$WORK/src" && SMOKE_DEPLOYMENT_OUTPUT="$WORK/deployments.yaml" SMOKE_APP_IMAGE="$APP_IMAGE" SMOKE_ROOT_IMAGE="$ROOT_IMAGE" go test ./internal/ingress -run TestEmitHostingResourceDeployments -count=1 >/dev/null); [[ -s "$WORK/deployments.yaml" ]] || { echo 'renderer did not emit Deployments' >&2; exit 1; }; rm -f "$WORK/src/internal/ingress/hosting_resources_smoke_generate_test.go"
docker run --privileged --name "$NAME" -d "$K3S_IMAGE" server --disable traefik --disable servicelb >/dev/null; created=1
for _ in $(seq 1 45); do if docker exec "$NAME" kubectl get node --no-headers 2>/dev/null | grep -q ' Ready '; then break; fi; sleep 2; done
docker exec "$NAME" kubectl get node --no-headers | grep -q ' Ready ' || { echo 'k3s did not become ready' >&2; exit 1; }; docker save "$APP_IMAGE" "$ROOT_IMAGE" | docker exec -i "$NAME" ctr -n k8s.io images import - >/dev/null
NAMESPACE=hosting-smoke
"$ROOT/scripts/create-hosting-namespace.sh" --namespace "$NAMESPACE" --cpu 100m --memory 64Mi --ephemeral-storage 64Mi --pods 10 >"$WORK/quota.yaml"
docker exec "$NAME" kubectl create namespace "$NAMESPACE" >/dev/null; docker cp "$WORK/quota.yaml" "$NAME:/tmp/quota.yaml"; docker cp "$WORK/deployments.yaml" "$NAME:/tmp/deployments.yaml"; docker exec "$NAME" kubectl apply -f /tmp/quota.yaml >/dev/null; docker exec "$NAME" kubectl apply -f /tmp/deployments.yaml >/dev/null
docker exec "$NAME" kubectl wait --for=condition=Available deployment/hosted-api -n "$NAMESPACE" --timeout=120s >/dev/null
phase=$(docker exec "$NAME" kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=hosted-api -o jsonpath='{.items[0].status.phase}'); [[ "$phase" = Running ]] || { echo "hosted-api is not Running: $phase" >&2; exit 1; }
process_uid=$(docker exec "$NAME" kubectl exec -n "$NAMESPACE" deployment/hosted-api -- id -u)
pod_uid=$(docker exec "$NAME" kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=hosted-api -o jsonpath='{.items[0].metadata.uid}')
restart_count=$(docker exec "$NAME" kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=hosted-api -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}')
nonroot=$(docker exec "$NAME" kubectl get deploy hosted-api -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].securityContext.runAsNonRoot}')
escalation=$(docker exec "$NAME" kubectl get deploy hosted-api -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].securityContext.allowPrivilegeEscalation}')
automount=$(docker exec "$NAME" kubectl get deploy hosted-api -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.automountServiceAccountToken}')
drop_caps=$(docker exec "$NAME" kubectl get deploy hosted-api -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].securityContext.capabilities.drop[0]}')
seccomp=$(docker exec "$NAME" kubectl get deploy hosted-api -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].securityContext.seccompProfile.type}')
cpu=$(docker exec "$NAME" kubectl get deploy hosted-api -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].resources.requests.cpu}')
memory=$(docker exec "$NAME" kubectl get deploy hosted-api -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].resources.limits.memory}')
[[ "$process_uid" != 0 && -n "$pod_uid" && "$restart_count" = 0 && "$nonroot" = true && "$escalation" = false && "$automount" = false && "$drop_caps" = ALL && "$seccomp" = RuntimeDefault && "$cpu" = 25m && "$memory" = 32Mi ]] || { echo "unexpected renderer/runtime settings: process_uid=$process_uid pod_uid=$pod_uid restarts=$restart_count nonroot=$nonroot escalation=$escalation automount=$automount caps=$drop_caps seccomp=$seccomp cpu=$cpu memory=$memory" >&2; exit 1; }
docker exec "$NAME" kubectl exec -n "$NAMESPACE" deployment/hosted-api -- wget -qO- -T 3 http://127.0.0.1:8080/ | grep -qx ok
cat >"$WORK/overflow.yaml" <<EOF2
apiVersion: v1
kind: Pod
metadata: {name: quota-overflow, namespace: hosting-smoke}
spec:
  containers:
  - name: app
    image: $APP_IMAGE
    resources:
      requests: {cpu: 90m, memory: 16Mi, ephemeral-storage: 16Mi}
      limits: {cpu: 90m, memory: 16Mi, ephemeral-storage: 16Mi}
EOF2
docker cp "$WORK/overflow.yaml" "$NAME:/tmp/overflow.yaml"
if docker exec "$NAME" kubectl apply -f /tmp/overflow.yaml >"$WORK/overflow.out" 2>&1; then echo 'quota admitted an over-budget Pod' >&2; exit 1; fi
if ! grep -Eiq 'exceeded quota' "$WORK/overflow.out" || ! grep -Eiq 'requests\.cpu' "$WORK/overflow.out"; then
  cat "$WORK/overflow.out" >&2
  exit 1
fi
docker exec "$NAME" kubectl wait --for=condition=Available deployment/hosted-api -n "$NAMESPACE" --timeout=30s >/dev/null
docker exec "$NAME" kubectl exec -n "$NAMESPACE" deployment/hosted-api -- wget -qO- -T 3 http://127.0.0.1:8080/ | grep -qx ok
post_restart_count=$(docker exec "$NAME" kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=hosted-api -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}')
post_pod_uid=$(docker exec "$NAME" kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=hosted-api -o jsonpath='{.items[0].metadata.uid}')
[[ "$post_restart_count" = 0 && "$post_pod_uid" = "$pod_uid" ]] || { echo "healthy app changed after quota rejection: uid=$post_pod_uid restarts=$post_restart_count" >&2; exit 1; }
root_reason=''; root_message=''
for _ in $(seq 1 30); do
  root_reason=$(docker exec "$NAME" kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=root-api -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.reason}' 2>/dev/null || true)
  root_message=$(docker exec "$NAME" kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=root-api -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.message}' 2>/dev/null || true)
  if [[ "$root_reason" = CreateContainerConfigError || "$root_reason" = CreateContainerError ]] && grep -Eiq 'runAsNonRoot|run as non-root|must not run as root|non-root' <<<"$root_message"; then break; fi
  sleep 2
done
if [[ "$root_reason" != CreateContainerConfigError && "$root_reason" != CreateContainerError ]] || ! grep -Eiq 'runAsNonRoot|run as non-root|must not run as root|non-root' <<<"$root_message"; then
  echo "root image was not rejected for nonroot policy: reason=$root_reason message=$root_message" >&2
  exit 1
fi
echo "hosting resources smoke passed ($K3S_IMAGE; quota rejection and root-image rejection verified)"
