# Seer

Offline-first, fault-tolerant job and cron monitoring.

Standard ping services drop telemetry or fire false "missed/failed" alerts when the network flaps. Seer uses local atomic persistence and durable replay so jobs keep running and outcomes still land — without false positives.

## Monorepo

| Path | Package | Role |
| ---- | ------- | ---- |
| [`sdks/python/`](sdks/python/) | **seerpy** | Python SDK — `monitor()`, heartbeats, offline queue |
| [`cli/`](cli/) | **seer** | Wrap any command: `seer run`, `heartbeat`, `replay` |
| [`server/`](server/) | **seer-server** | Go Community Edition ingest + basic alerts |

Python SDK and CLI share the same envelope format and default queue path (`~/.seer/queue`, override with `SEER_QUEUE_DIR`).

## Quick start

### Python SDK

```bash
cd sdks/python
pip install -e ".[dev]"
```

```python
from seerpy import Seer

seer = Seer(api_key="YOUR_SEER_API_KEY", auto_replay=True)
with seer.monitor("daily_etl", capture_logs=True):
    ...
```

### CLI

```bash
cd cli
go build -o seer .
export SEER_API_KEY=your_key
./seer run daily_etl -- python etl.py
```

### Community Edition server

```bash
cd server
go run ./cmd/seer-server
# or: docker build -t seer-server . && docker run -p 8080:8080 seer-server
```

## Community vs Enterprise

| Free / CE | Enterprise |
| --------- | ---------- |
| Core monitoring agent, telemetry ingest | Team RBAC |
| Basic Slack / email alerts | Okta / SSO |
| Offline-first SDK + CLI | PagerDuty / Datadog sync |
| SQLite self-host | SOC 2 audit logging, VPC runners |

CE exposes `/enterprise/*` stubs that return a stable paywall response so clients can detect edition without bundling EE code.

## CI / CD

Workflows are package-scoped (no cross-repo sync):

- `python-sdk.yml` — test + PyPI publish from `sdks/python`
- `cli.yml` — Go test + release binaries from `cli`
- `server.yml` — Go test/build + GHCR Docker image from `server`

## License

Copyright (c) 2025 Ansr Studio. See [LICENSE](LICENSE).
