#!/usr/bin/env bash
# Live smoke test for seer-cli (bash). Prefer examples/live_smoke_test.ps1 on Windows.
#
#   export SEER_API_KEY=your_key
#   export SEER_BASE_URL=https://api.ansrstudio.com   # optional
#   export SEER_JOB_NAME=your_dashboard_job_name
#   bash examples/live_smoke_test.sh

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
CLI_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"

section() {
  echo
  echo "============================================================"
  echo "$1"
  echo "============================================================"
}

require_env() {
  if [ -z "${!1:-}" ]; then
    echo "Set $1 before running this script."
    exit 1
  fi
}

section "0) Build local seer binary"
(
  cd "$CLI_DIR"
  go build -o seer .
)
SEER="$CLI_DIR/seer"
chmod +x "$SEER"

require_env SEER_API_KEY
BASE_URL="${SEER_BASE_URL:-https://api.ansrstudio.com}"
JOB_NAME="${SEER_JOB_NAME:-a}"
HEARTBEAT_NAME="${SEER_HEARTBEAT_NAME:-$JOB_NAME}"
QUEUE_DIR="$(mktemp -d -t seer-cli-smoke-XXXXXX)"
export SEER_QUEUE_DIR="$QUEUE_DIR"
export SEER_TIMEOUT="${SEER_TIMEOUT:-30}"

echo "queue_dir=$QUEUE_DIR"
echo "job_name=$JOB_NAME base_url=$BASE_URL"

failures=0
expect_exit() {
  actual="$1"; expected="$2"; label="$3"
  if [ "$actual" -ne "$expected" ]; then
    echo "FAIL: $label (exit $actual, expected $expected)"
    failures=$((failures + 1))
  else
    echo "OK: $label (exit $actual)"
  fi
}

section "1) version"
"$SEER" version || true
expect_exit $? 0 "version"

section "2) run success"
set +e
"$SEER" run "$JOB_NAME" \
  --base-url="$BASE_URL" \
  --metadata='{"suite":"cli_live_smoke","case":"success"}' \
  --tags=smoke,success,cli \
  -- sh -c "echo hello from cli smoke; sleep 0.4"
expect_exit $? 0 "success run"
set -e

section "3) run failure preserves exit"
set +e
"$SEER" run "$JOB_NAME" \
  --base-url="$BASE_URL" \
  --metadata='{"suite":"cli_live_smoke","case":"failure"}' \
  --tags=smoke,failure,cli \
  -- sh -c "echo about to fail; exit 42"
expect_exit $? 42 "failed run exit 42"
set -e

section "4) heartbeat"
set +e
"$SEER" heartbeat "$HEARTBEAT_NAME" \
  --base-url="$BASE_URL" \
  --metadata="{\"suite\":\"cli_live_smoke\",\"pid\":$$}" \
  --tags=smoke,heartbeat,cli
expect_exit $? 0 "heartbeat"
set -e

section "5) replay"
set +e
"$SEER" replay --base-url="$BASE_URL"
expect_exit $? 0 "replay"
set -e

section "6) offline unreachable host"
set +e
"$SEER" run "$JOB_NAME" \
  --base-url="https://127.0.0.1:9" \
  --no-auto-replay \
  --metadata='{"suite":"cli_live_smoke","case":"offline"}' \
  --tags=smoke,offline,cli \
  -- sh -c "echo still runs offline"
expect_exit $? 0 "offline run"
set -e
queued="$(find "$QUEUE_DIR" -maxdepth 1 -name '*.json' | wc -l | tr -d ' ')"
echo "Queued envelopes: $queued"
[ "$queued" -ge 1 ] || { echo "FAIL: expected queued envelope"; failures=$((failures + 1)); }

section "7) background-replay"
# clear bad pinned envelopes first
rm -f "$QUEUE_DIR"/*.json "$QUEUE_DIR"/dead/*.json 2>/dev/null || true
stamp="$(date -u +%Y%m%d%H%M%S000000)"
cat > "$QUEUE_DIR/${stamp}_heartbeat_smoke.json" <<EOF
{
  "version": 3,
  "endpoint": "heartbeat",
  "base_url": "${BASE_URL%/}",
  "created_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "attempts": 0,
  "idempotency_key": "$(uuidgen 2>/dev/null || echo bg-key-1)",
  "payload": {
    "job_name": "$HEARTBEAT_NAME",
    "current_time": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "metadata": {"suite": "cli_live_smoke", "case": "background"},
    "tags": ["smoke", "background", "cli"]
  }
}
EOF
set +e
"$SEER" run "$JOB_NAME" \
  --base-url="$BASE_URL" \
  --no-auto-replay \
  --background-replay \
  --replay-interval=2 \
  --metadata='{"suite":"cli_live_smoke","case":"background_replay"}' \
  --tags=smoke,background,cli \
  -- sh -c "echo waiting; sleep 5"
expect_exit $? 0 "background-replay"
set -e
"$SEER" replay --base-url="$BASE_URL" >/dev/null || true

section "DONE"
echo "Live smoke finished with $failures assertion failure(s)."
echo "Temp queue: $QUEUE_DIR"
[ "$failures" -eq 0 ]
