# SEER

**Offline-first job monitoring — not another ping service.**

Generic cron pings disappear when the network flaps, then fire false **missed / failed** alerts. **SEER** is different: it persists outcomes locally with atomic writes, keeps your process exit code intact, and delivers the real result when connectivity returns — no false positives from a blip.

**Cloud:** [seer.ansrstudio.com](https://seer.ansrstudio.com) — dashboard, API keys, pipelines, managed webhooks/email  
**This repo:** Python SDK · CLI · Community Edition self-host server (+ embedded ops UI)

## Quickstart

**CLI (any command)**

```bash
export SEER_API_KEY=your_key
go build -C cli -o seer . && ./seer run daily_etl -- python etl.py
# self-host: export SEER_BASE_URL=http://127.0.0.1:8080
```

**CE Fiber server**

```bash
export SEER_API_KEYS=dev-key
go run -C server ./cmd/seer-server
# UI http://127.0.0.1:8080/ui  ·  health http://127.0.0.1:8080/health
```

**Docker Compose**

```bash
export SEER_API_KEYS=dev-key
docker compose up --build
```

```python
# One-liners that ship to production
with Seer(api_key=KEY, auto_replay=True).monitor("daily_etl"): run()          # Python
@app.task(base=SeerTask)                                                      # Celery
def etl(): ...
```

```bash
seer run daily_etl -- python etl.py   # any language via CLI
```

---

## Why not a generic ping?

| Generic “I’m alive” ping           | SEER                                            |
| ---------------------------------- | ----------------------------------------------- |
| Silence = failure (noisy)          | Silence = wait; outcome is queued               |
| Network flap → false alert         | Atomic offline queue → replay                   |
| Often drops mid-run context        | Full lifecycle: running → success/failed + logs |
| Monitoring outage can fail the job | Your exit code / exception always wins          |

---

## Atomic offline queue

When upload fails, SEER never leaves a half-written envelope on disk. Writers use temp + rename so readers only ever see complete JSON:

```text
  ~/.seer/queue/
       │
       │  1) write envelope to temp
       ▼
  job.json.<uuid>.tmp  ──os.replace / rename──►  job.json     (pending)
       │                                              │
       │                                              │  2) claim for send
       │                                              ▼
       │                                         job.json.sending
       │                                              │
       │                         success ─────────────┼──► delete
       │                         fail (retries left) ─┼──► job.json (attempts++)
       │                         fail (max attempts) ─┴──► dead/job.json
       │
  replay / seer queue flush / auto_replay / background_replay
       │
       ▼
  SEER Cloud (api.ansrstudio.com)  or  CE server (:8080)
```

Shared by **seerpy** and the **CLI**. Caps: `SEER_QUEUE_MAX_FILES` / `SEER_QUEUE_MAX_BYTES` (FIFO eviction). Inspect with `seer queue status` · `list-dead` · `retry-dead`.

```mermaid
flowchart LR
  job[Your job] -->|POST fails| tmp["*.json.tmp"]
  tmp -->|atomic rename| pending["*.json"]
  pending -->|claim| sending["*.sending"]
  sending -->|OK| done[Delivered]
  sending -->|retry| pending
  sending -->|max attempts| dead["dead/*.json"]
  dead -->|retry-dead| pending
  pending -->|flush| api[SEER API]
```

---

## Instrument in seconds

**Python**

```python
from seerpy import Seer
seer = Seer(api_key="YOUR_KEY", auto_replay=True)  # cloud default host
with seer.monitor("daily_etl", capture_logs=True):
    run_pipeline()
```

**Celery** (`pip install seerpy[celery]`)

```python
from seerpy.integrations.celery import SeerTask, set_default_seer
set_default_seer(Seer(api_key="YOUR_KEY", auto_replay=True))

@app.task(base=SeerTask, seer_capture_logs=True)
def daily_etl():
    ...
```

**CLI — any runtime**

```bash
export SEER_API_KEY=your_key
seer run daily_etl -- python etl.py
seer run daily_etl -- node worker.js
seer heartbeat long_worker
seer queue status
```

Point at self-host with `SEER_BASE_URL=http://127.0.0.1:8080` (or `base_url=` in Python).

---

## Environment variables

### Clients (CLI + seerpy)

| Variable | Purpose | Default |
| -------- | ------- | ------- |
| `SEER_API_KEY` | API key (required for agents) | — |
| `SEER_BASE_URL` | API host | `https://api.ansrstudio.com` |
| `SEER_QUEUE_DIR` | Offline queue directory | `~/.seer/queue` |
| `SEER_QUEUE_MAX_FILES` | Max queued envelopes (FIFO eviction) | `500` |
| `SEER_QUEUE_MAX_BYTES` | Max queue size in bytes | `52428800` (50 MiB) |
| `SEER_TIMEOUT` | HTTP timeout seconds (CLI) | `30` |
| `SEER_REPLAY_JITTER_MS` | Max startup jitter before auto-replay | `2000` |

### CE server (`seer-server`)

| Variable | Purpose | Default |
| -------- | ------- | ------- |
| `SEER_PORT` | Listen port shortcut (`8080` → `:8080`) | `8080` via `SEER_HTTP_ADDR` |
| `SEER_HTTP_ADDR` | Full listen address (wins over `SEER_PORT`) | `:8080` |
| `SEER_DB_PATH` | SQLite database path | `data/seer.db` |
| `SEER_API_KEYS` | Comma-separated ingest/UI keys (or `SEER_API_KEY`) | — |
| `SEER_UI_ENABLED` | Serve embedded `/ui` | `true` |
| `SEER_UI_SECRET` | Cookie signing secret | derived from API keys |
| `SEER_WEBHOOK_URL` | Global JSON webhook (`SEER_SLACK_WEBHOOK_URL` still accepted) | — |
| `SEER_SMTP_HOST` | SMTP host (STARTTLS on 587; TLS on 465) | — |
| `SEER_SMTP_PORT` | SMTP port | `587` |
| `SEER_SMTP_USER` / `SEER_SMTP_PASS` | SMTP auth | — |
| `SEER_SMTP_FROM` / `SEER_SMTP_TO` | Envelope from / global recipient | from←user |
| `SEER_NOTIFY_ON_START` | Default job: alert on start | `false` |
| `SEER_NOTIFY_ON_SUCCESS` | Default job: alert on success | `false` |
| `SEER_NOTIFY_ON_FAILURE` | Default job: alert on failed/cancelled | `true` |
| `SEER_NOTIFY_ON_HEARTBEAT_MISSED` | Default job: alert on stale heartbeat | `true` |
| `SEER_HEARTBEAT_STALE_AFTER` | Seconds without heartbeat before miss | `300` |
| `SEER_HEARTBEAT_CHECK_INTERVAL` | In-process miss scan interval (`0` = off) | `0` |

Health: `GET /health` → `{"status":"ok","edition":"community","version":"<build>"}`.

---

## Choose your path

### Hosted — [seer.ansrstudio.com](https://seer.ansrstudio.com)

1. Create an account and a pipeline name
2. Copy your API key
3. Use the SDK/CLI with the default API host (`https://api.ansrstudio.com`)

### Self-hosted Community Edition

```bash
export SEER_API_KEYS=dev-key && go run -C server ./cmd/seer-server
# or: docker compose up --build
# UI → http://localhost:8080/ui/login  (key: dev-key)
```

Jobs auto-create on first event. Generic webhook + SMTP, per-job notify flags, heartbeat miss checks, and `/ui` ship in one binary.

---

## Community vs Enterprise

|               | **Community** (this repo)           | **Enterprise / Cloud**                                       |
| ------------- | ----------------------------------- | ------------------------------------------------------------ |
| Agents        | seerpy + CLI, offline queue, Celery | Same agents against managed API                              |
| Ingest        | Self-host Go CE + SQLite            | Hosted at [seer.ansrstudio.com](https://seer.ansrstudio.com) |
| UI            | Embedded `/ui` ops console          | Full product dashboard                                       |
| Alerts        | Generic webhook + SMTP              | Managed channels + routing                                   |
| Auth / teams  | Shared API keys                     | RBAC, Okta / SSO (Coming Soon)                               |
| Incident sync | —                                   | PagerDuty / Datadog (Coming Soon)                            |
| Compliance    | —                                   | VPC (Coming Soon)                                            |

CE `/enterprise/*` returns a stable **402** so clients can detect edition without bundling EE code.

---

## Monorepo

| Path                           | Package         | Role                                     |
| ------------------------------ | --------------- | ---------------------------------------- |
| [`sdks/python/`](sdks/python/) | **seerpy**      | SDK — monitor, heartbeats, queue, Celery |
| [`cli/`](cli/)                 | **seer**        | Wrap any command; `queue` diagnostics    |
| [`server/`](server/)           | **seer-server** | CE ingest + alerts + UI                  |

---

## Give to an agent

Paste into Cursor (or any coding agent) to stand up and verify the full local stack:

````markdown
# SEER local stack — agent runbook

Work in this monorepo. Start Community Edition, exercise CLI + Python against it, confirm UI. Do not commit unless asked.

## Prerequisites

- Go 1.22+, Python 3.10+
- Repo root contains `cli/`, `sdks/python/`, `server/`

## 1) Start CE server (background)

```powershell
cd server
$env:SEER_API_KEYS = "dev-key"
$env:SEER_DB_PATH = "$(Get-Location)\data\agent-seer.db"
$env:SEER_HTTP_ADDR = ":8080"
$env:SEER_NOTIFY_ON_FAILURE = "true"
go run ./cmd/seer-server
```

Wait for listen/UI logs. Check: `Invoke-RestMethod http://127.0.0.1:8080/health`

## 2) CLI jobs

```powershell
cd cli
go build -o seer.exe .
$env:SEER_API_KEY = "dev-key"
$env:SEER_BASE_URL = "http://127.0.0.1:8080"
$env:SEER_REPLAY_JITTER_MS = "0"
$env:SEER_QUEUE_DIR = "$env:TEMP\seer-agent-queue"
New-Item -ItemType Directory -Force -Path $env:SEER_QUEUE_DIR | Out-Null
.\seer.exe run agent_etl --tags=agent -- powershell -NoProfile -Command "Write-Host ok; exit 0"
.\seer.exe run agent_fail --tags=agent -- powershell -NoProfile -Command "Write-Host boom; exit 1"
.\seer.exe heartbeat agent_worker --metadata='{\"tick\":1}'
.\seer.exe queue status
```

## 3) Python + tests

```powershell
cd sdks/python
pip install -e ".[dev,celery]" -q
$env:SEER_API_KEY = "dev-key"
$env:SEER_BASE_URL = "http://127.0.0.1:8080"
$env:SEER_REPLAY_JITTER_MS = "0"
python -c "from seerpy import Seer; s=Seer(api_key='dev-key', base_url='http://127.0.0.1:8080', auto_replay=True)
with s.monitor('agent_py', capture_logs=True):
    print('hello from seerpy')"
python -m pytest tests/ -q
```

## 4) Server tests

```powershell
cd server
go test ./...
```

## 5) UI

Open http://127.0.0.1:8080/ui/login — key `dev-key`. Confirm demo jobs/runs.

## Cloud

https://seer.ansrstudio.com — use dashboard API key; omit SEER_BASE_URL (defaults to https://api.ansrstudio.com). Cloud jobs must exist in the dashboard; CE auto-creates.

## Success

- health ok · CLI exit codes preserved · pytest + `go test` green · UI shows jobs
````

---

## CI

- `python-sdk.yml` — test + PyPI (`sdks/python`)
- `cli.yml` — Go test + binaries (`cli`); tag `cli-v*` for legacy GH release
- `server.yml` — Go test/build + GHCR (`server`); tag `server-v*` for image
- `release.yml` — GoReleaser cross-platform CLI + server archives on `v*` tags

---

## License

[MIT](LICENSE) © Ansr Studio
