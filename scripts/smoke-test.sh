#!/bin/sh
set -eu

DEPLOYER_BIN="${DEPLOYER_BIN:-deployer}"
SERVER_URL="${DEPLOYER_SERVER_URL:-}"
ADMIN_TOKEN="${DEPLOYER_ADMIN_TOKEN:-}"
APP_NAME="${DEPLOYER_SMOKE_APP:-deployer-smoke}"
IMAGE="${DEPLOYER_SMOKE_IMAGE:-nginx:alpine}"
ARCH="${DEPLOYER_SMOKE_ARCH:-linux/arm64}"

if [ -z "$SERVER_URL" ] || [ -z "$ADMIN_TOKEN" ]; then
  echo "set DEPLOYER_SERVER_URL and DEPLOYER_ADMIN_TOKEN before running the smoke test" >&2
  exit 2
fi

WORKDIR="$(mktemp -d)"
cleanup() {
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

cat > "$WORKDIR/deployer.yaml" <<EOF
name: $APP_NAME
image: $IMAGE
service:
  port: 80
  health:
    path: /
routing: {}
deploy:
  replicas: 1
placement:
  arch: $ARCH
state:
  mode: stateless
resilience:
  mode: basic
EOF

"$DEPLOYER_BIN" --server "$SERVER_URL" --token "$ADMIN_TOKEN" server status
"$DEPLOYER_BIN" --server "$SERVER_URL" --token "$ADMIN_TOKEN" doctor
"$DEPLOYER_BIN" --server "$SERVER_URL" --token "$ADMIN_TOKEN" deploy --file "$WORKDIR/deployer.yaml"
"$DEPLOYER_BIN" --server "$SERVER_URL" --token "$ADMIN_TOKEN" status "$APP_NAME"
"$DEPLOYER_BIN" --server "$SERVER_URL" --token "$ADMIN_TOKEN" logs --tail 20 "$APP_NAME"

echo "smoke test completed for $APP_NAME"
