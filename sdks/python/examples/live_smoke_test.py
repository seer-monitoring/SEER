"""
Live smoke test for seerpy against a real Seer API.

Usage:
  set SEER_API_KEY=your_key
  optional: set SEER_BASE_URL=https://api.seer.dev
  python examples/live_smoke_test.py
"""

from __future__ import annotations

import os
import sys
import tempfile
import time
from pathlib import Path

# Allow running from a source checkout without installing first.
ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

try:
    from dotenv import load_dotenv

    load_dotenv(ROOT / ".env")
except ImportError:
    pass

from seerpy import Seer


def require_api_key() -> str:
    key = os.getenv("SEER_API_KEY") or os.getenv("SEER_APIKEY")
    if not key:
        print("Set SEER_API_KEY before running this script.")
        print("  PowerShell:  $env:SEER_API_KEY = 'seer_...'")
        print("  bash:        export SEER_API_KEY=seer_...")
        sys.exit(1)
    return key


def section(title: str) -> None:
    print("\n" + "=" * 60)
    print(title)
    print("=" * 60)


def main() -> None:
    api_key = require_api_key()
    base_url = os.getenv("SEER_BASE_URL")  # optional; defaults to https://api.seer.dev
    job_name = os.getenv("SEER_JOB_NAME", "a")
    heartbeat_name = os.getenv("SEER_HEARTBEAT_NAME", job_name)

    # Isolate this run's offline queue so we don't touch the user's real queue.
    queue_dir = tempfile.mkdtemp(prefix="seer-smoke-queue-")
    os.environ["SEER_QUEUE_DIR"] = queue_dir
    print(f"Using temp queue dir: {queue_dir}")
    print(f"Using job_name={job_name!r} heartbeat_name={heartbeat_name!r}")
    if base_url:
        print(f"Using base_url from env: {base_url}")
    else:
        print("Using default base_url (set SEER_BASE_URL to override)")

    # ------------------------------------------------------------------
    # 1) Init + one-shot auto_replay (empty queue is fine)
    # ------------------------------------------------------------------
    section("1) Init with auto_replay=True")
    seer = Seer(
        api_key=api_key,
        base_url=base_url,
        auto_replay=True,
        timeout=30,
    )
    print(f"Connected client base_url={seer.base_url}")

    # ------------------------------------------------------------------
    # 2) Successful monitor + log capture + tags/metadata
    # ------------------------------------------------------------------
    section("2) monitor() success path")
    with seer.monitor(
        job_name,
        capture_logs=True,
        metadata={"suite": "live_smoke", "case": "success"},
        tags=["smoke", "success"],
    ):
        print("hello from smoke_success")
        time.sleep(0.5)

    # ------------------------------------------------------------------
    # 3) Failed monitor (exception should still propagate)
    # ------------------------------------------------------------------
    section("3) monitor() failure path (expects exception)")
    try:
        with seer.monitor(
            job_name,
            capture_logs=True,
            metadata={"suite": "live_smoke", "case": "failure"},
            tags=["smoke", "failure"],
        ):
            print("about to fail on purpose")
            raise RuntimeError("intentional smoke-test failure")
    except RuntimeError as exc:
        print(f"Caught expected error: {exc}")

    # ------------------------------------------------------------------
    # 4) Heartbeat
    # ------------------------------------------------------------------
    section("4) heartbeat()")
    seer.heartbeat(
        heartbeat_name,
        metadata={"suite": "live_smoke", "pid": os.getpid()},
        tags=["smoke", "heartbeat"],
    )

    # ------------------------------------------------------------------
    # 5) Manual replay (should be a no-op / empty unless something failed)
    # ------------------------------------------------------------------
    section("5) replay() on current queue")
    result = seer.replay()
    print(
        f"ReplayResult sent={result.sent} failed={result.failed} "
        f"dead_lettered={result.dead_lettered} skipped={result.skipped}"
    )

    # ------------------------------------------------------------------
    # 6) Offline final (no run_id) → replay registers then completes
    # ------------------------------------------------------------------
    section("6) offline envelope without run_id + replay")
    from seerpy.payloads import save_failed_payload
    from datetime import datetime, timezone
    import uuid

    idem = str(uuid.uuid4())
    queued_path = save_failed_payload(
        {
            "job_name": job_name,
            "status": "success",
            "run_id": "",  # never reached Seer at start
            "start_time": datetime.now(timezone.utc).isoformat(sep=" "),
            "end_time": datetime.now(timezone.utc).isoformat(sep=" "),
            "metadata": {"suite": "live_smoke", "case": "offline_replay"},
            "error_details": None,
            "tags": ["smoke", "offline"],
            "logs": "queued offline then replayed\n",
        },
        "monitoring",
        idempotency_key=idem,
        base_url=seer.base_url,
    )
    print(f"Queued envelope at {queued_path}")
    print(f"Idempotency-Key={idem} (replay will use :register then :complete)")

    result = seer.replay()
    print(
        f"After offline replay: sent={result.sent} failed={result.failed} "
        f"dead_lettered={result.dead_lettered}"
    )
    remaining = list(Path(queue_dir).glob("*.json"))
    print(f"Queue files remaining: {len(remaining)}")

    # ------------------------------------------------------------------
    # 7) Background flusher (short interval)
    # ------------------------------------------------------------------
    section("7) background_replay=True")
    bg = Seer(
        api_key=api_key,
        base_url=base_url,
        background_replay=True,
        replay_interval=2,
        timeout=30,
    )
    save_failed_payload(
        {
            "job_name": heartbeat_name,
            "current_time": datetime.now(timezone.utc).isoformat(sep=" "),
            "metadata": {"suite": "live_smoke", "case": "background"},
            "tags": ["smoke", "background"],
        },
        "heartbeat",
        base_url=bg.base_url,
    )
    print("Waiting for background flusher...")
    time.sleep(5)
    bg.stop_background_replay()
    remaining = list(Path(queue_dir).glob("*.json"))
    print(f"Queue files remaining after background flush: {len(remaining)}")

    # ------------------------------------------------------------------
    # 8) Unreachable base_url should queue, not crash the job
    # ------------------------------------------------------------------
    section("8) unreachable base_url (queues final result)")
    offline = Seer(
        api_key=api_key,
        base_url="https://127.0.0.1:9",  # nothing listening
        timeout=1,
    )
    # Avoid long backoff during smoke: monkeypatch retries for this instance.
    from seerpy import http as seer_http

    original = seer_http.post_with_backoff

    def _fast_fail(*args, **kwargs):
        kwargs["max_retries"] = 1
        kwargs["timeout"] = 1
        return original(*args, **kwargs)

    seer_http.post_with_backoff = _fast_fail
    try:
        with offline.monitor(job_name, capture_logs=True):
            print("job still runs while Seer is unreachable")
    finally:
        seer_http.post_with_backoff = original

    queued = list(Path(queue_dir).glob("*.json"))
    print(f"Queued after unreachable host: {len(queued)} file(s)")
    for path in queued:
        print(f"  - {path.name}")

    section("8b) envelope base_url pinning (expect fail against 127.0.0.1)")
    pinned = seer.replay(max_attempts=1)
    print(
        f"Pinned replay: sent={pinned.sent} failed={pinned.failed} "
        f"dead_lettered={pinned.dead_lettered}"
    )

    section("DONE")
    print("Live smoke test finished.")
    print(f"Temp queue left at: {queue_dir}")
    print(
        "If you saw 'Job Does Not Exist' / 'Pipeline Does Not Exist', "
        "create that job in the Seer dashboard and re-run with:\n"
        "  $env:SEER_JOB_NAME = 'your_exact_job_name'"
    )


if __name__ == "__main__":
    main()
