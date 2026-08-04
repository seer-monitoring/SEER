# Seer Server (Community Edition)

Lightweight Go ingest server for Seer monitoring telemetry.

- Fiber + GORM + pure-Go SQLite (`glebarez/sqlite`)
- Statically linked (`CGO_ENABLED=0`)
- Embedded ops UI at `/ui` (jobs, runs, settings, alert channels)
- Generic JSON webhook + SMTP alerts (start / success / failure / cancelled / heartbeat miss)
- Heartbeat staleness check via `GET /check_heartbeat` or optional in-process ticker
- `/enterprise/*` paywall stubs for EE features
- Distroless Docker image + Compose; GoReleaser binaries on tag

## Quickstart

```bash
export SEER_API_KEYS=dev-key
go run ./cmd/seer-server
# → http://127.0.0.1:8080/ui  ·  GET /health
```

```bash
# from repo root
export SEER_API_KEYS=dev-key
docker compose up --build
```

Open the UI: [http://localhost:8080/ui](http://localhost:8080/ui) and sign in with `dev-key` (any key from `SEER_API_KEYS`).

## Run locally (alerts)

```bash
export SEER_API_KEYS=dev-key
export SEER_DB_PATH=./data/seer.db
# Alerts (optional — without these, failures are logged as "not delivered")
export SEER_WEBHOOK_URL=https://your.example/hooks/seer
export SEER_SMTP_HOST=smtp.example.com
export SEER_SMTP_PORT=587
export SEER_SMTP_USER=user
export SEER_SMTP_PASS=pass
export SEER_SMTP_FROM=seer@example.com
export SEER_SMTP_TO=ops@example.com
go run ./cmd/seer-server
```

Webhook POSTs JSON like:

```json
{
  "event": "job.failed",
  "job_name": "daily_etl",
  "status": "failed",
  "run_id": "...",
  "error_details": "...",
  "subject": "Job daily_etl had a failed event.",
  "text": "Pipeline: daily_etl\nStatus: failed\n..."
}
```

Discord webhook URLs are auto-adapted (`content` field). Extra UI channels fan out in addition to `SEER_WEBHOOK_URL` / `SEER_SMTP_TO`.

## Ops UI

Embedded in the same binary (no separate frontend process):

| Page | What you can do |
| ---- | --------------- |
| `/ui/login` | Sign in with an API key |
| `/ui/` | Job list, last status, heartbeat stale badge, trigger heartbeat check |
| `/ui/jobs/:name` | Edit notify flags / stale interval, see heartbeats and recent runs |
| `/ui/runs/:run_id` | Run detail: logs, errors, metadata, tags |
| `/ui/channels` | Add / enable / delete webhook or email alert channels |

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
| `SEER_PORT` | Port shortcut (`8080` → `:8080`; overridden by `SEER_HTTP_ADDR`) |
| `SEER_HTTP_ADDR` | Listen address (default `:8080`) |
| `SEER_DB_PATH` | SQLite path (default `data/seer.db`) |
| `SEER_API_KEYS` | Comma-separated API keys (or `SEER_API_KEY`) |
| `SEER_UI_ENABLED` | Serve embedded UI (default `true`) |
| `SEER_UI_SECRET` | Cookie signing secret (default derived from API keys) |
| `SEER_WEBHOOK_URL` | Generic JSON webhook URL for alerts (`SEER_SLACK_WEBHOOK_URL` still accepted) |
| `SEER_SMTP_HOST` / `SEER_SMTP_PORT` | SMTP relay (587 uses STARTTLS; 465 uses TLS) |
| `SEER_SMTP_USER` / `SEER_SMTP_PASS` | SMTP auth |
| `SEER_SMTP_FROM` / `SEER_SMTP_TO` | Email envelope |
| `SEER_NOTIFY_ON_START` | Alert on new run (`true`/`false`, default `false`) |
| `SEER_NOTIFY_ON_SUCCESS` | Alert on success (default `false`) |
| `SEER_NOTIFY_ON_FAILURE` | Alert on failed/cancelled (default `true`) |
| `SEER_NOTIFY_ON_HEARTBEAT_MISSED` | Alert on stale heartbeat (default `true`) |
| `SEER_HEARTBEAT_STALE_AFTER` | Seconds without heartbeat before miss (default `300`) |
| `SEER_HEARTBEAT_CHECK_INTERVAL` | In-process miss scan interval in seconds (`0` = off, use cron → `/check_heartbeat`) |

## Health

```bash
curl -s http://127.0.0.1:8080/health
# {"status":"ok","edition":"community","version":"dev"}
```

`version` is injected at build time (`-ldflags` / GoReleaser / Docker `VERSION` build-arg).

## Docker

Multi-stage **distroless** image (see [`Dockerfile`](Dockerfile)). From the monorepo root:

```bash
export SEER_API_KEYS=dev-key
# optional alerts:
# export SEER_WEBHOOK_URL=...
# export SEER_SMTP_HOST=... SEER_SMTP_PORT=587 SEER_SMTP_USER=... SEER_SMTP_PASS=...
# export SEER_SMTP_FROM=... SEER_SMTP_TO=...
docker compose up --build
```

SQLite persists in the `seer_data` volume at `/data/seer.db`.  
(Client offline queues use `SEER_QUEUE_DIR` on agents — not the server container.)

Manual build:

```bash
docker build -t seer-server-ce --build-arg VERSION=v1.0.0 .
docker run --rm -p 8080:8080 \
  -e SEER_API_KEYS=dev-key \
  -e SEER_PORT=8080 \
  -v seer-data:/data \
  seer-server-ce
```

## Enterprise stubs

Authenticated requests to `/enterprise/<feature>` return HTTP 402 with:

```json
{"error":"enterprise_required","edition":"community","feature":"..."}
```

Covered features include RBAC, SSO/Okta, PagerDuty/Datadog sync, and audit logging.
