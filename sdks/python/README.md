# SeerPy

SeerPy is the official Python client for the Seer Monitoring API by Ansr Studio. It helps you monitor jobs, capture logs, record heartbeats, and reliably report execution results — including when the network is down.

**Current version: 0.2.4**

## Installation

```bash
pip install seerpy
```

Dev / tests:

```bash
pip install -e ".[dev]"
pytest
```

---

## What's new in 0.2.x

This release rebuilds the offline path and hardens the client. Summary of changes from the earlier 0.1.x line:

### Offline queue redesign

- Failed uploads are stored as **versioned envelopes** (not raw JSON blobs named only by endpoint).
- Queue lives in **`~/.seer/queue`** (was inside the package directory). Override with `SEER_QUEUE_DIR`.
- Each envelope includes: `endpoint`, `payload`, `created_at`, `attempts`, `idempotency_key`, and `base_url`.
- **Atomic writes** (`tmp` + `os.replace`) so readers never see partial files.
- **Cross-process locking** via `filelock` during replay; claim-by-rename (`.sending`) avoids double-sends.
- **FIFO eviction** when the queue exceeds limits (default **500 files** / **50 MiB**). Override with `SEER_QUEUE_MAX_FILES` / `SEER_QUEUE_MAX_BYTES`.
- After repeated failures, envelopes move to **`~/.seer/queue/dead/`**.

### Correct offline monitor behavior

- If Seer is unreachable at **start**, the client no longer queues a forever-`running` stub.
- The job still runs locally; when it finishes, the **final** outcome (`success`/`failed`, logs, traceback) is queued.
- Empty `run_id` means “never registered with Seer.” On replay, those events **register first** to obtain a `run_id`, then post the completion.
- If start succeeded and only the completion upload failed, replay posts the completion with the existing `run_id`.

### Replay tiers

| Mode                     | Behavior                                                            |
| ------------------------ | ------------------------------------------------------------------- |
| `seer.replay()`          | Manual flush anytime                                                |
| `auto_replay=True`       | Flush once on client init                                           |
| `background_replay=True` | Daemon thread flushes periodically (`replay_interval`, default 60s) |

### Idempotency

- Every live POST and queued envelope carries a UUID v4 `idempotency_key`.
- Sent as the **`Idempotency-Key`** header so timeouts/retries do not create duplicate runs.
- Offline monitoring replay uses `{key}:register` then `{key}:complete`.

### Configurable API host

- Default: **`https://api.ansrstudio.com/`**
- Override with `base_url=` or **`SEER_BASE_URL`** (explicit arg wins).
- Envelopes **pin** the `base_url` they were created for, so later config changes do not mis-route retries.

### Reliability / correctness fixes

- Monitoring never raises from `finally` — Seer outages cannot mask your job’s exception or fail the job.
- HTTP **4xx** are not retried (except **429**); **5xx** and connection errors use **full-jitter** exponential backoff (optional `Retry-After` on 429).
- Auto-replay / background flush apply startup jitter (`SEER_REPLAY_JITTER_MS`, default 2000) to avoid reconnect stampedes.
- Response JSON parsing handles both dict and string bodies (no double-decode crash).
- Log capture **restores** prior logging handlers/levels (no longer clears `logger.handlers`).
- Shared `requests.Session`, configurable timeouts, consolidated HTTP helper.
- `tags` supported on `monitor()` / `heartbeat()`.
- `api_key=` preferred; `apiKey=` kept for compatibility.
- Optional **Celery** integration: `pip install seerpy[celery]` → `SeerTask` / `connect_seer_signals`.

### Packaging & hygiene

- New dependency: **`filelock`**
- Removed unused nested `setup.py` and accidental scratch scripts
- Added `.gitignore`, root test suite (`tests/`), live smoke script (`examples/live_smoke_test.py`)

---

## Getting started

```python
from seerpy import Seer

# Default host: https://api.ansrstudio.com/
seer = Seer(api_key="YOUR_SEER_API_KEY", auto_replay=True)
```

`apiKey=` is still accepted for backwards compatibility.

### Custom / self-hosted endpoints

```python
# Self-hosted Seer, enterprise proxy, or local mock
seer = Seer(api_key="...", base_url="https://seer.internal.company.com")
```

```powershell
$env:SEER_BASE_URL = "https://api.ansrstudio.com"   # example override
```

---

## Monitor jobs

`job_name` must match a pipeline that already exists in your Seer dashboard.

```python
with seer.monitor(
    "daily_etl_job",
    capture_logs=True,
    metadata={"source": "data-lake"},
    tags=["etl", "prod"],
):
    print("Running ETL job...")
```

Seer captures:

- Start and end timestamps
- Status (`running` → `success` / `failed`)
- Logs (when `capture_logs=True`)
- Error traceback (on failure)

Monitoring never fails your job. If Seer is down, the final result is queued for replay.

---

## Heartbeats

```python
seer.heartbeat("worker_process", metadata={"pid": 1234, "status": "active"})
```

---

## Offline support & replay

```python
from seerpy import Seer

# One-shot flush on startup
seer = Seer(api_key="YOUR_SEER_API_KEY", auto_replay=True)

# Ideal for long-running servers
seer = Seer(api_key="...", background_replay=True, replay_interval=60)

result = seer.replay()
print(result.sent, result.failed, result.dead_lettered)

seer.stop_background_replay()  # optional clean shutdown
```

### Environment variables

| Variable               | Purpose                                           |
| ---------------------- | ------------------------------------------------- |
| `SEER_API_KEY`         | API key (app-level; pass into `Seer(...)`)        |
| `SEER_BASE_URL`        | Override default API host                         |
| `SEER_QUEUE_DIR`       | Offline queue directory (default `~/.seer/queue`) |
| `SEER_QUEUE_MAX_FILES` | Max queued envelopes (default `500`)              |
| `SEER_QUEUE_MAX_BYTES` | Max queue size in bytes (default `50 MiB`)        |
| `SEER_REPLAY_JITTER_MS`| Max startup jitter before auto-replay (default `2000`) |

```python
from dotenv import load_dotenv
import os
from seerpy import Seer, queue_status, retry_dead

load_dotenv()
seer = Seer(api_key=os.getenv("SEER_API_KEY"), auto_replay=True)
print(queue_status())
retry_dead(api_key=os.getenv("SEER_API_KEY"), all_dead=True)
```

(`python-dotenv` is optional; install separately if you use `.env` files.)

---

## Celery

```bash
pip install seerpy[celery]
```

```python
from celery import Celery
from seerpy import Seer
from seerpy.integrations.celery import SeerTask, set_default_seer

seer = Seer(api_key="...", auto_replay=True)
set_default_seer(seer)

app = Celery("workers")

@app.task(base=SeerTask, seer_capture_logs=True)
def etl():
    ...
```

Or wire all tasks via signals: `connect_seer_signals(seer)`.

See `examples/celery_demo.py`.

Community Edition backends already support generic JSON webhooks + SMTP email; PagerDuty/Opsgenie/Datadog sync remain Enterprise.

---

## API reference

| Method                                                                                                     | Description                   |
| ---------------------------------------------------------------------------------------------------------- | ----------------------------- |
| `Seer(api_key, auto_replay=False, background_replay=False, replay_interval=60, base_url=None, timeout=30)` | Create a client               |
| `monitor(job_name, capture_logs=False, metadata=None, tags=None)`                                          | Context manager for a job run |
| `heartbeat(job_name, metadata=None, tags=None)`                                                            | Liveness signal               |
| `replay(max_attempts=5)`                                                                                   | Flush the offline queue       |
| `start_background_replay()` / `stop_background_replay()`                                                   | Control the periodic flusher  |

---

## Example: full worker script

```python
import os
from seerpy import Seer

seer = Seer(
    api_key=os.getenv("SEER_API_KEY"),
    auto_replay=True,
    background_replay=True,
    replay_interval=60,
)

def run_job():
    with seer.monitor("example_worker", capture_logs=True, metadata={"env": "prod"}):
        print("Starting work...")
        for i in range(3):
            print(f"step {i+1}")
        seer.heartbeat("example_worker", metadata={"progress": "50%"})
        print("Work complete.")

if __name__ == "__main__":
    run_job()
```

## Live smoke test

```powershell
$env:SEER_API_KEY = "your_key"
$env:SEER_BASE_URL = "https://api.ansrstudio.com"   # if not using the default host
$env:SEER_JOB_NAME = "your_dashboard_job_name"
python examples/live_smoke_test.py
```

Exercises success/failure monitoring, heartbeats, offline queue, background replay, and `base_url` pinning.

---

## License

[MIT](../../LICENSE) © Ansr Studio

## About

Seer is a monitoring and observability platform built by Ansr Studio. Learn more at: https://ansrstudio.com
