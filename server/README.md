# Seer Server (Community Edition)

Lightweight Go ingest server for Seer monitoring telemetry.

- Fiber + GORM + pure-Go SQLite (`glebarez/sqlite`)
- Statically linked (`CGO_ENABLED=0`)
- Embedded ops UI at `/ui` (jobs, runs, settings, alert channels)
- Slack webhook + SMTP alerts (start / success / failure / cancelled / heartbeat miss)
- Heartbeat staleness check via `GET /check_heartbeat` or optional in-process ticker
- `/enterprise/*` paywall stubs for EE features

## Run locally

```bash
export SEER_API_KEYS=dev-key
export SEER_DB_PATH=./data/seer.db
go run ./cmd/seer-server
```

Open the UI: [http://localhost:8080/ui](http://localhost:8080/ui) and sign in with `dev-key` (any key from `SEER_API_KEYS`).

```bash
curl -s localhost:8080/health
curl -s -X POST localhost:8080/monitoring \
  -H "Authorization: dev-key" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo:register" \
  -d '{"job_name":"demo","status":"running","run_id":"","start_time":"2026-01-01T00:00:00Z"}'
```

## Ops UI

Embedded in the same binary (no separate frontend process):

| Page | What you can do |
| ---- | --------------- |
| `/ui/login` | Sign in with an API key |
| `/ui/` | Job list, last status, heartbeat stale badge, trigger heartbeat check |
| `/ui/jobs/:name` | Edit notify flags / stale interval, see heartbeats and recent runs |
| `/ui/runs/:run_id` | Run detail: logs, errors, metadata, tags |
| `/ui/channels` | Add / enable / delete Slack webhook or email alert channels |

Session cookie is httpOnly; ingest routes still use `Authorization` headers.

## Monitoring logic

| Request | Behavior |
| ------- | -------- |
| `status=running` without `run_id` | Create run; optional **start** alert |
| `status=running` with `run_id` | Progress upsert (metadata/tags/logs); **no** alert |
| `status=success\|failed\|cancelled` | Complete run (or create offline terminal run); alert per gates |
| `POST /heartbeat` | Upsert last-seen; clears miss-alert debounce |
| `GET /check_heartbeat` | Alert jobs whose last heartbeat is past the stale threshold |

Jobs are auto-created on first event. Notification flags and stale interval are copied from env defaults at create time.

## Environment

| Variable | Purpose |
| -------- | ------- |
| `SEER_HTTP_ADDR` | Listen address (default `:8080`) |
| `SEER_DB_PATH` | SQLite path (default `data/seer.db`) |
| `SEER_API_KEYS` | Comma-separated API keys (or `SEER_API_KEY`) |
| `SEER_UI_ENABLED` | Serve embedded UI (default `true`) |
| `SEER_UI_SECRET` | Cookie signing secret (default derived from API keys) |
| `SEER_SLACK_WEBHOOK_URL` | Slack incoming webhook |
| `SEER_SMTP_HOST` / `SEER_SMTP_PORT` | SMTP relay |
| `SEER_SMTP_USER` / `SEER_SMTP_PASS` | SMTP auth |
| `SEER_SMTP_FROM` / `SEER_SMTP_TO` | Email envelope |
| `SEER_NOTIFY_ON_START` | Alert on new run (`true`/`false`, default `false`) |
| `SEER_NOTIFY_ON_SUCCESS` | Alert on success (default `false`) |
| `SEER_NOTIFY_ON_FAILURE` | Alert on failed/cancelled (default `true`) |
| `SEER_NOTIFY_ON_HEARTBEAT_MISSED` | Alert on stale heartbeat (default `true`) |
| `SEER_HEARTBEAT_STALE_AFTER` | Seconds without heartbeat before miss (default `300`) |
| `SEER_HEARTBEAT_CHECK_INTERVAL` | In-process miss scan interval in seconds (`0` = off, use cron → `/check_heartbeat`) |

## Docker

```bash
docker build -t seer-server .
docker run --rm -p 8080:8080 \
  -e SEER_API_KEYS=dev-key \
  -v seer-data:/data \
  seer-server
```

## Enterprise stubs

Authenticated requests to `/enterprise/<feature>` return HTTP 402 with:

```json
{"error":"enterprise_required","edition":"community","feature":"..."}
```

Covered features include RBAC, SSO/Okta, PagerDuty/Datadog sync, and audit logging.
