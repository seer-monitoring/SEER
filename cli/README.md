# Seer CLI

Official command-line client for the [Seer Monitoring API](https://ansrstudio.com) by Ansr Studio.

Wrap any command in any language, send heartbeats, and reliably report results — including when the network is down.

**Current version: 0.2.4** (aligned with SeerPy 0.2.4 offline semantics)

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/seer-monitoring/seer-cli/main/cli/install.sh | sh
```

Or build from source:

```bash
cd cli
go build -o seer .
```

## Quick start

```bash
export SEER_API_KEY=your_key

# Monitor any command (job_name must exist in your Seer dashboard)
seer run daily_etl --metadata='{"source":"lake"}' --tags=etl,prod -- python etl.py

# Heartbeat
seer heartbeat worker_process --metadata='{"pid":1234}'

# Flush the offline queue
seer replay
```

PowerShell:

```powershell
$env:SEER_API_KEY = "your_key"
seer run daily_etl --tags=etl,prod -- python etl.py
```

---

## What's new in 0.2.x

Parity with SeerPy's offline redesign and reliability fixes:

### Offline queue redesign

- Failed uploads are stored as **versioned envelopes** (not raw JSON blobs named only by endpoint).
- Queue lives in **`~/.seer/queue`** (shared with SeerPy). Override with `SEER_QUEUE_DIR`.
- Each envelope includes: `endpoint`, `payload`, `created_at`, `attempts`, `idempotency_key`, and `base_url`.
- **Atomic writes** (`tmp` + rename) so readers never see partial files.
- **Cross-process locking** during replay; claim-by-rename (`.sending`) avoids double-sends.
- **FIFO eviction** when the queue exceeds limits (default **500 files** / **50 MiB**).
- After repeated failures, envelopes move to **`~/.seer/queue/dead/`**.

### Correct offline monitor behavior

- If Seer is unreachable at **start**, the CLI no longer queues a forever-`running` stub.
- The command still runs locally; when it finishes, the **final** outcome is queued.
- Empty `run_id` means “never registered.” On replay, those events **register first**, then post completion.
- Your command’s **exit code is preserved** — Seer outages never fail the job.

### Replay

| Mode | Behavior |
| ---- | -------- |
| `seer replay` | Manual flush anytime |
| Auto-replay (default on `run` / `heartbeat`) | Flush once on start (`--no-auto-replay` to skip) |
| `--background-replay` | Periodic flush while `seer run` is executing |

### Idempotency

- Every live POST and queued envelope carries a UUID v4 `Idempotency-Key`.
- Offline monitoring replay uses `{key}:register` then `{key}:complete`.

### Configurable API host

- Default: `https://api.ansrstudio.com`
- Override with `--base-url=` or `SEER_BASE_URL`.
- Envelopes **pin** the `base_url` they were created for.

---

## Commands

### `seer run`

```bash
seer run <job-name> [flags] [--] <command> [args...]
```

| Flag | Description |
| ---- | ----------- |
| `--capture-logs=true\|false` | Capture stdout/stderr (default `true`) |
| `--metadata=<json>` | JSON object attached to the run |
| `--tags=a,b,c` | Comma-separated tags (or JSON array) |
| `--base-url=<url>` | Override API host |
| `--no-auto-replay` | Skip one-shot queue flush on start |
| `--background-replay` | Flush queue periodically while the job runs |
| `--replay-interval=<sec>` | Background interval (default `60`) |

### `seer heartbeat`

```bash
seer heartbeat <job-name> [--metadata=<json>] [--tags=a,b] [--base-url=<url>]
```

### `seer replay`

```bash
seer replay [--max-attempts=5] [--base-url=<url>]
```

`replay-failed` is kept as an alias for compatibility. Prefer `seer queue flush` for the same behavior.

### `seer queue`

```bash
seer queue status
seer queue flush [--max-attempts=5]
seer queue list-dead
seer queue retry-dead --all
seer queue retry-dead dead_item.json --no-flush
```

Inspect pending / `.sending` / `dead/` envelopes, flush the queue, and re-queue dead letters (attempts reset to 0).

---

## Environment variables

| Variable | Purpose |
| -------- | ------- |
| `SEER_API_KEY` | API key (required) |
| `SEER_BASE_URL` | Override default API host |
| `SEER_QUEUE_DIR` | Offline queue directory (default `~/.seer/queue`) |
| `SEER_QUEUE_MAX_FILES` | Max queued envelopes (default `500`; raise toward `1000` if needed) |
| `SEER_QUEUE_MAX_BYTES` | Max queue size in bytes (default `50 MiB`; raise toward `100 MiB` if needed) |
| `SEER_TIMEOUT` | HTTP timeout seconds (default `30`) |
| `SEER_REPLAY_JITTER_MS` | Max random delay before auto-replay / first background flush (default `2000`) |

## Live smoke test

```powershell
$env:SEER_API_KEY = "your_key"
$env:SEER_BASE_URL = "https://api.ansrstudio.com"   # if not using the default host
$env:SEER_JOB_NAME = "your_dashboard_job_name"
pwsh -File cli/examples/live_smoke_test.ps1
```

Exercises success/failure monitoring, exit-code preservation, heartbeats, tags/metadata, auto-replay, `--no-auto-replay`, `--capture-logs=false`, offline queue + base_url pinning, register-then-complete replay, and `--background-replay`.

Bash:

```bash
export SEER_API_KEY=your_key
export SEER_JOB_NAME=your_dashboard_job_name
bash cli/examples/live_smoke_test.sh
```

## Build release binaries

```powershell
pwsh -File cli/build_all.ps1
# -> cli/builds/seer-{darwin,linux,windows}-{amd64,arm64}[.exe]
```

```bash
bash cli/build_all.sh
```

---

## Shared queue with SeerPy

The CLI and Python SDK use the **same envelope format** and default queue path (`~/.seer/queue`). Events queued by either client can be flushed by either:

```bash
seer replay
# or
python -c "from seerpy import Seer; print(Seer(api_key=...).replay())"
```

---

## Dev / tests

```bash
cd cli
go test ./...
go build -o seer .
```

---

## NOTICE

Sources were imported from [`seer-monitoring/seer-cli`](https://github.com/seer-monitoring/seer-cli) at commit `2dc7bee` into this monorepo (`cli/`) to remove cross-repo sync overhead. Build from this directory:

```bash
cd cli && go build -o seer .
```

## License

Copyright (c) 2025 Ansr Studio.  
All rights reserved. Use of this software is permitted solely for connecting to and interacting with the Seer API. Redistribution, modification, or any other use of this code is prohibited without written permission from Ansr Studio.
