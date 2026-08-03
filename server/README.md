# Seer Server (Community Edition)

Lightweight Go ingest server for Seer monitoring telemetry.

- Fiber + GORM + pure-Go SQLite (`glebarez/sqlite`)
- Statically linked (`CGO_ENABLED=0`)
- Basic Slack webhook + SMTP alerts on failed runs
- `/enterprise/*` paywall stubs for EE features

## Run locally

```bash
export SEER_API_KEYS=dev-key
export SEER_DB_PATH=./data/seer.db
go run ./cmd/seer-server
```

```bash
curl -s localhost:8080/health
curl -s -X POST localhost:8080/monitoring \
  -H "Authorization: dev-key" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo:register" \
  -d '{"job_name":"demo","status":"running","run_id":"","start_time":"2026-01-01T00:00:00Z"}'
```

## Environment

| Variable | Purpose |
| -------- | ------- |
| `SEER_HTTP_ADDR` | Listen address (default `:8080`) |
| `SEER_DB_PATH` | SQLite path (default `data/seer.db`) |
| `SEER_API_KEYS` | Comma-separated API keys (or `SEER_API_KEY`) |
| `SEER_SLACK_WEBHOOK_URL` | Slack incoming webhook for failure alerts |
| `SEER_SMTP_HOST` / `SEER_SMTP_PORT` | SMTP relay |
| `SEER_SMTP_USER` / `SEER_SMTP_PASS` | SMTP auth |
| `SEER_SMTP_FROM` / `SEER_SMTP_TO` | Email envelope |

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
