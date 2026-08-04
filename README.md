# SEER

**Job monitoring that survives network failure.**

SEER watches cron jobs, ETL pipelines, workers, and long-running batch work. When the network flaps, standard ping services drop telemetry or fire false “missed” alerts. SEER keeps a durable local queue, preserves your process exit code, and delivers the real outcome when connectivity returns.

**Cloud product:** [seer.ansrstudio.com](https://seer.ansrstudio.com) — dashboard, API keys, pipelines, Slack/email, and managed alerting.

**This repository** is the open client + Community Edition stack: Python SDK, CLI, and a self-hosted Go ingest server with an embedded ops UI.

---

## Why SEER

| Problem                                  | What SEER does                                           |
| ---------------------------------------- | -------------------------------------------------------- |
| Network blip → false “job missed”        | Offline queue + idempotent register/complete             |
| Monitoring outage fails your job         | Uploads never override your exit code / exception        |
| Cron wrappers only work for one language | CLI wraps **any** command; Python gets a first-class SDK |
| Self-host needs a dashboard              | CE server ships ingest **and** `/ui` in one binary       |

```text
Your job runs locally
    │
    ├─► register running  ──┐
    ├─► heartbeats        ──┼──► SEER Cloud  or  CE server
    └─► success / failed  ──┘
              │
         if offline → ~/.seer/queue → replay later
```

---

## Choose your path

### Hosted (recommended for most teams)

1. Open **[seer.ansrstudio.com](https://seer.ansrstudio.com)** and create an account.
2. Create a pipeline / job name in the dashboard.
3. Copy your API key.
4. Point the SDK or CLI at the cloud API (default host — no `SEER_BASE_URL` needed):

```bash
export SEER_API_KEY=your_key
# default: https://api.ansrstudio.com
```

Docs and account UI live on the cloud site. This repo is how you **instrument** jobs against that API (or against self-hosted CE).

### Self-hosted Community Edition

Run the Go server locally or in Docker. Jobs auto-create on first event. Sign into the embedded UI with your API key.

```bash
cd server
export SEER_API_KEYS=dev-key
go run ./cmd/seer-server
# UI → http://localhost:8080/ui/login  (key: dev-key)
```

CE includes Slack webhook + SMTP alerts, per-job notify settings, heartbeat miss checks, and queue-friendly ingest. Enterprise features (RBAC, SSO, PagerDuty/Datadog sync, audit logging) stay behind `/enterprise/*` paywall stubs.

---

## Monorepo map

| Path                           | Package         | Role                                                        |
| ------------------------------ | --------------- | ----------------------------------------------------------- |
| [`sdks/python/`](sdks/python/) | **seerpy**      | Python SDK — `monitor()`, heartbeats, offline queue, Celery |
| [`cli/`](cli/)                 | **seer**        | Wrap any command — `run`, `heartbeat`, `queue`, `replay`    |
| [`server/`](server/)           | **seer-server** | CE ingest + alerts + embedded ops UI                        |

SDK and CLI share the same envelope format and default queue path (`~/.seer/queue`).

---

## Quick start

### Python

```bash
cd sdks/python
pip install -e ".[dev]"
```

```python
from seerpy import Seer

# Cloud: use your seer.ansrstudio.com API key (default host)
# Self-host: Seer(api_key="dev-key", base_url="http://127.0.0.1:8080", auto_replay=True)
seer = Seer(api_key="YOUR_SEER_API_KEY", auto_replay=True)

with seer.monitor("daily_etl", capture_logs=True, tags=["prod"]):
    run_pipeline()
```

Celery: `pip install seerpy[celery]` — see [`sdks/python/README.md`](sdks/python/README.md).

### CLI

```bash
cd cli
go build -o seer .
export SEER_API_KEY=your_key
./seer run daily_etl -- python etl.py
./seer heartbeat worker --metadata='{"pid":1}'
./seer queue status
```

### Community Edition server

```bash
cd server
export SEER_API_KEYS=dev-key
go run ./cmd/seer-server
```

Open [http://localhost:8080/ui](http://localhost:8080/ui) → log in with `dev-key`.

---

## Product surface

**Clients**

- Register → complete lifecycle with logs, metadata, tags
- Heartbeats for long-running work
- Offline queue with FIFO caps, dead-letter, jittered retry/replay
- CLI queue tools: `status`, `flush`, `list-dead`, `retry-dead`

**Community Edition server**

- `/monitoring`, `/heartbeat`, `/check_heartbeat`
- Embedded UI: jobs, runs, notify settings, alert channels
- Slack webhook + SMTP (env and/or UI channels)

**Cloud** ([seer.ansrstudio.com](https://seer.ansrstudio.com))

- Managed dashboard, pipelines, keys, and team workflows
- Production alerting and hosted reliability

---

## Give to an agent

Paste the block below into Cursor (or any coding agent) to stand up and verify the full local stack.

````markdown
# SEER local stack — agent runbook

You are working in the SEER monorepo. Goal: start Community Edition, exercise CLI + Python SDK against it, and confirm the UI. Do not commit unless asked.

## 0) Prerequisites

- Go 1.22+, Python 3.10+, network for first `pip`/`go` module fetch
- Workspace root: this repo (contains `cli/`, `sdks/python/`, `server/`)

## 1) Start CE server (background)

```powershell
cd server
$env:SEER_API_KEYS = "dev-key"
$env:SEER_DB_PATH = "$(Get-Location)\data\agent-seer.db"
$env:SEER_HTTP_ADDR = ":8080"
$env:SEER_NOTIFY_ON_FAILURE = "true"
$env:SEER_REPLAY_JITTER_MS = "0"   # only for clients; server ignores this
go run ./cmd/seer-server
```

Wait until logs show listening / UI enabled. Health check:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health
```

## 2) Build CLI and run jobs

```powershell
cd cli
go build -o seer.exe .
$env:SEER_API_KEY = "dev-key"
$env:SEER_BASE_URL = "http://127.0.0.1:8080"
$env:SEER_REPLAY_JITTER_MS = "0"
$env:SEER_QUEUE_DIR = "$env:TEMP\seer-agent-queue"
New-Item -ItemType Directory -Force -Path $env:SEER_QUEUE_DIR | Out-Null

.\seer.exe run agent_etl --tags=agent,demo -- powershell -NoProfile -Command "Write-Host 'etl ok'; exit 0"
.\seer.exe run agent_fail --tags=agent,demo -- powershell -NoProfile -Command "Write-Host 'boom'; exit 1"
.\seer.exe heartbeat agent_worker --metadata='{\"tick\":1}'
.\seer.exe queue status
```

## 3) Python SDK smoke

```powershell
cd sdks/python
pip install -e ".[dev,celery]" -q
$env:SEER_API_KEY = "dev-key"
$env:SEER_BASE_URL = "http://127.0.0.1:8080"
$env:SEER_REPLAY_JITTER_MS = "0"
python -c "from seerpy import Seer; s=Seer(api_key='dev-key', base_url='http://127.0.0.1:8080', auto_replay=True);
exec('with s.monitor(\"agent_py\", capture_logs=True):\n print(\"hello from seerpy\")')"
python -m pytest tests/ -q
```

## 4) Server unit tests

```powershell
cd server
go test ./...
```

## 5) UI check

Open http://127.0.0.1:8080/ui/login — API key `dev-key`. Confirm jobs `agent_etl`, `agent_fail`, `agent_worker`, `agent_py` appear with runs/heartbeats.

## 6) Optional cloud note

Cloud dashboard is https://seer.ansrstudio.com — for cloud, set SEER_API_KEY from the dashboard and omit SEER_BASE_URL (defaults to https://api.ansrstudio.com). Job names must exist in the cloud dashboard; CE auto-creates jobs.

## Success criteria

- /health returns ok
- CLI success + failure runs complete; failure preserves non-zero exit
- pytest green; server go test green
- UI lists the demo jobs after login
````

## CI

Package-scoped workflows (no cross-repo sync):

- `python-sdk.yml` — test + PyPI from `sdks/python`
- `cli.yml` — Go test + release binaries from `cli`
- `server.yml` — Go test/build + GHCR image from `server`

---

## License

Copyright © Ansr Studio. See [LICENSE](LICENSE).

Use of the clients is permitted for connecting to the Seer API (cloud or self-hosted). Redistribution and other uses require written permission.
