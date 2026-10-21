#!/usr/bin/env bash
# One-command deploy: build frontend, cross-compile, upload, restart the unit.
#
#   ./deploy.sh              # uses .deploy.env (gitignored) if present
#   ./deploy.sh --dry-run    # build locally and print the remote commands only
#   DEPLOY_HOST=root@h ./deploy.sh   # or override per invocation
#
# Server details live in .deploy.env, which is gitignored, so the repository never
# carries a host address. Copy .deploy.env.example to get started.
set -euo pipefail

cd "$(dirname "$0")"

if [ -f .deploy.env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.deploy.env
  set +a
fi

DEPLOY_PATH="${DEPLOY_PATH:-/srv/comic/server}"
DEPLOY_UNIT="${DEPLOY_UNIT:-comic}"
DRY_RUN=0

if [ "${1:-}" = "--dry-run" ]; then
  DRY_RUN=1
  DEPLOY_HOST="${DEPLOY_HOST:-root@dry-run.invalid}"
fi

: "${DEPLOY_HOST:?missing DEPLOY_HOST — create .deploy.env (see .deploy.env.example)}"

step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

step "building frontend"
make frontend

step "building linux binary (static, stripped, no local paths)"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o server-linux .

printf '    %s\n' "$(ls -lh server-linux | awk '{print $5}')"

step "uploading to ${DEPLOY_HOST}:${DEPLOY_PATH}"
if [ "$DRY_RUN" = "1" ]; then
  echo "    (dry run) rsync -avzP ./server-linux ${DEPLOY_HOST}:${DEPLOY_PATH}"
  echo "    (dry run) ssh ${DEPLOY_HOST} \"systemctl restart ${DEPLOY_UNIT}\""
  step "dry run finished — nothing was uploaded"
  exit 0
fi

rsync -avzP ./server-linux "${DEPLOY_HOST}:${DEPLOY_PATH}"

step "restarting ${DEPLOY_UNIT}"
ssh "${DEPLOY_HOST}" "systemctl restart ${DEPLOY_UNIT}"

step "checking /healthz"
host="${DEPLOY_HOST#*@}"
probe_ok=0
last=""

# Probe the public site when one is configured: the ssh host is usually a bare IP,
# and https against an IP fails TLS because the certificate belongs to the domain.
targets=()
if [ -n "${PUBLIC_URL:-}" ]; then
  targets+=("${PUBLIC_URL%/}/healthz")
else
  targets+=("https://${host}/healthz" "http://${host}/healthz")
fi

# Retry: `systemctl restart` returns before the process is listening, and a proxy
# holding a keep-alive connection to the previous process can answer 502 once.
# A single probe reports failure for a deployment that is actually fine.
for attempt in $(seq 1 20); do
  # https first: the host sits behind a TLS-terminating proxy, and over http that
  # proxy answers 308, which curl treats as success — a false positive.
  for url in "${targets[@]}"; do
    body="$(curl -fsSL --max-time 5 "${url}" 2>/dev/null || true)"
    last="${body:-<empty>}"

    if [ "${body}" = "ok" ]; then
      echo "    ${url} -> ok (after ${attempt}s)"
      probe_ok=1
      break 2
    fi
  done

  sleep 1
done

if [ "${probe_ok}" = "0" ]; then
  echo "    /healthz did not return 'ok' after 20s (last: ${last})"
  echo "    check: ssh ${DEPLOY_HOST} journalctl -u ${DEPLOY_UNIT} -n 50"
fi

step "done"
