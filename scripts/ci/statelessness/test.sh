#!/usr/bin/env bash
# scripts/ci/statelessness/test.sh
#
# Proves that two API instances sharing Postgres + NATS are stateless:
#   1. Submit 10 jobs through nginx (round-robin) and verify all complete.
#   2. Kill api1; submit 5 more jobs and verify api2 handles them alone.
#
# Requires: docker, curl, jq.
# Usage: ./test.sh [--no-build]
#
# Exit codes:
#   0 — all assertions passed
#   1 — test failure or infrastructure error

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NGINX_URL="http://localhost:8090"
COMPOSE="docker compose -f ${SCRIPT_DIR}/docker-compose.yml"
TIMEOUT_SECS=120
NO_BUILD="${1:-}"

cleanup() {
  echo "--- tearing down"
  $COMPOSE down --remove-orphans -v 2>/dev/null || true
}
trap cleanup EXIT

# ---- 1. Start infrastructure ------------------------------------------------

echo "--- starting services"
if [[ "$NO_BUILD" == "--no-build" ]]; then
  $COMPOSE up -d
else
  $COMPOSE up -d --build
fi

# Wait for nginx to accept connections (proxy passes to at least one api).
echo "--- waiting for nginx"
for i in $(seq 1 60); do
  if curl -sf "${NGINX_URL}/v1/health" >/dev/null 2>&1; then
    echo "    nginx ready after ${i}s"
    break
  fi
  if [[ $i -eq 60 ]]; then
    echo "ERROR: nginx not ready after 60s" >&2
    $COMPOSE logs api1 api2 nginx
    exit 1
  fi
  sleep 1
done

# ---- helpers ----------------------------------------------------------------

# Minimal pipeline.Config JSON with a passthrough no-op graph.
PASSTHROUGH_JOB=$(cat <<'JSON'
{
  "schema_version": "1.4",
  "config": {
    "inputs":  [{"id":"in","uri":"file:///dev/null"}],
    "outputs": [{"id":"out","uri":"file:///dev/null","codec":"copy"}],
    "graph":   [{"type":"passthrough","inputs":["in"],"outputs":["out"]}]
  }
}
JSON
)

submit_job() {
  curl -sf -X POST "${NGINX_URL}/v1/jobs" \
       -H "Content-Type: application/json" \
       -d "${PASSTHROUGH_JOB}" \
    | jq -r '.id'
}

poll_job() {
  local id="$1"
  local deadline=$((SECONDS + TIMEOUT_SECS))
  while [[ $SECONDS -lt $deadline ]]; do
    status=$(curl -sf "${NGINX_URL}/v1/jobs/${id}" | jq -r '.status' 2>/dev/null || echo "unknown")
    case "$status" in
      succeeded) return 0 ;;
      failed)
        echo "ERROR: job ${id} failed" >&2
        return 1
        ;;
    esac
    sleep 2
  done
  echo "ERROR: job ${id} timed out after ${TIMEOUT_SECS}s (last status: ${status})" >&2
  return 1
}

assert_all_complete() {
  local ids=("$@")
  local failed=0
  for id in "${ids[@]}"; do
    echo "    polling job ${id}"
    if ! poll_job "$id"; then
      failed=1
    fi
  done
  return $failed
}

# ---- 2. Phase 1: 10 jobs through both api instances -------------------------

echo "--- submitting 10 jobs via nginx (round-robin over api1+api2)"
JOB_IDS=()
for i in $(seq 1 10); do
  id=$(submit_job)
  echo "    submitted job ${id}"
  JOB_IDS+=("$id")
done

echo "--- waiting for all 10 jobs to complete"
assert_all_complete "${JOB_IDS[@]}"
echo "    PASS: all 10 jobs completed"

# ---- 3. Phase 2: kill api1, verify api2 handles new jobs alone --------------

echo "--- killing api1 container"
docker stop mm_api1

echo "--- submitting 5 more jobs (only api2 available)"
FAILOVER_IDS=()
for i in $(seq 1 5); do
  id=$(submit_job)
  echo "    submitted job ${id}"
  FAILOVER_IDS+=("$id")
done

echo "--- waiting for failover jobs to complete via api2"
assert_all_complete "${FAILOVER_IDS[@]}"
echo "    PASS: all 5 failover jobs completed via api2"

echo ""
echo "=== Phase C statelessness test PASSED ==="
