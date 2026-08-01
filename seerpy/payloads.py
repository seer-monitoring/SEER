"""Offline payload queue with atomic writes and cross-process locking."""

from __future__ import annotations

import json
import os
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional, Tuple

from filelock import FileLock, Timeout

from .http import parse_json_response, post_with_backoff

ENVELOPE_VERSION = 3
DEFAULT_BASE_URL = "https://api.ansrstudio.com/"
DEFAULT_MAX_ATTEMPTS = 5
DEFAULT_MAX_QUEUE_FILES = 500
DEFAULT_MAX_QUEUE_BYTES = 50 * 1024 * 1024  # 50 MiB
ENDPOINT_PATHS = {
    "monitoring": "/monitoring",
    "heartbeat": "/heartbeat",
}


def resolve_base_url(explicit: Optional[str] = None) -> str:
    """Resolve API base URL: explicit arg > SEER_BASE_URL env > default."""
    if explicit:
        return explicit.rstrip("/")
    env = os.environ.get("SEER_BASE_URL", "").strip()
    if env:
        return env.rstrip("/")
    return DEFAULT_BASE_URL.rstrip("/")


def get_queue_dir() -> str:
    override = os.environ.get("SEER_QUEUE_DIR")
    if override:
        return os.path.abspath(override)
    return os.path.join(os.path.expanduser("~"), ".seer", "queue")


def _env_int(name: str, default: int) -> int:
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return default
    try:
        value = int(raw)
    except ValueError:
        return default
    return value if value > 0 else default


def get_queue_limits() -> Tuple[int, int]:
    """Return (max_files, max_bytes) for the offline queue."""
    return (
        _env_int("SEER_QUEUE_MAX_FILES", DEFAULT_MAX_QUEUE_FILES),
        _env_int("SEER_QUEUE_MAX_BYTES", DEFAULT_MAX_QUEUE_BYTES),
    )


def _ensure_queue_dir(queue_dir: Optional[str] = None) -> str:
    path = queue_dir or get_queue_dir()
    os.makedirs(path, exist_ok=True)
    os.makedirs(os.path.join(path, "dead"), exist_ok=True)
    return path


def _utc_now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _atomic_write_json(filepath: str, data: Dict[str, Any]) -> None:
    """Write JSON via temp file + os.replace so readers never see partial files."""
    directory = os.path.dirname(filepath)
    os.makedirs(directory, exist_ok=True)
    tmp_path = f"{filepath}.{uuid.uuid4().hex}.tmp"
    try:
        with open(tmp_path, "w", encoding="utf-8") as handle:
            json.dump(data, handle, indent=2)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_path, filepath)
    finally:
        if os.path.exists(tmp_path):
            try:
                os.remove(tmp_path)
            except OSError:
                pass


def _list_queue_files(path: str) -> List[str]:
    """FIFO order: filenames include UTC timestamps so lexicographic sort is oldest-first."""
    return sorted(
        name
        for name in os.listdir(path)
        if name.endswith(".json") and not name.endswith(".sending")
    )


def _file_size(path: str) -> int:
    try:
        return os.path.getsize(path)
    except OSError:
        return 0


def enforce_queue_limits(
    queue_dir: Optional[str] = None,
    *,
    max_files: Optional[int] = None,
    max_bytes: Optional[int] = None,
) -> int:
    """Evict oldest envelopes until under file/byte caps. Returns number evicted."""
    path = _ensure_queue_dir(queue_dir)
    default_files, default_bytes = get_queue_limits()
    max_files = default_files if max_files is None else max_files
    max_bytes = default_bytes if max_bytes is None else max_bytes

    lock = FileLock(os.path.join(path, ".queue.lock"), timeout=5)
    evicted = 0
    with lock:
        while True:
            files = _list_queue_files(path)
            sizes = {name: _file_size(os.path.join(path, name)) for name in files}
            total_bytes = sum(sizes.values())
            if len(files) <= max_files and total_bytes <= max_bytes:
                break
            # Keep at least the newest envelope even if a single file exceeds max_bytes.
            if len(files) <= 1:
                break
            oldest = files[0]
            oldest_path = os.path.join(path, oldest)
            try:
                os.remove(oldest_path)
                evicted += 1
                print(
                    f"Seer queue limit reached; evicted oldest envelope: {oldest}"
                )
            except OSError:
                break
    return evicted


def save_failed_payload(
    payload: Dict[str, Any],
    endpoint: str,
    *,
    queue_dir: Optional[str] = None,
    idempotency_key: Optional[str] = None,
    base_url: Optional[str] = None,
) -> str:
    """Persist a failed upload as a versioned envelope. Returns the file path."""
    if endpoint not in ENDPOINT_PATHS:
        raise ValueError(f"Unknown endpoint: {endpoint}")

    path = _ensure_queue_dir(queue_dir)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S%f")
    # Timestamp first so lexicographic sort is true FIFO across endpoints.
    filename = f"{stamp}_{endpoint}_{uuid.uuid4().hex[:8]}.json"
    filepath = os.path.join(path, filename)
    envelope = {
        "version": ENVELOPE_VERSION,
        "endpoint": endpoint,
        "base_url": resolve_base_url(base_url),
        "payload": payload,
        "created_at": _utc_now_iso(),
        "attempts": 0,
        "idempotency_key": idempotency_key or str(uuid.uuid4()),
    }
    _atomic_write_json(filepath, envelope)
    enforce_queue_limits(path)
    print(f"Seer upload failed, queued at {filepath}")
    print("Call seer.replay() or initialize with auto_replay=True to retrigger events.")
    return filepath


def _load_envelope(filepath: str) -> Dict[str, Any]:
    with open(filepath, "r", encoding="utf-8") as handle:
        data = json.load(handle)

    # Legacy: raw payload files without an envelope wrapper
    if "payload" not in data or "endpoint" not in data:
        basename = os.path.basename(filepath)
        if "monitoring" in basename:
            endpoint = "monitoring"
        elif "heartbeat" in basename:
            endpoint = "heartbeat"
        else:
            raise ValueError(f"Cannot infer endpoint for legacy file: {basename}")
        return {
            "version": 0,
            "endpoint": endpoint,
            "base_url": resolve_base_url(),
            "payload": data,
            "created_at": _utc_now_iso(),
            "attempts": 0,
            "idempotency_key": str(uuid.uuid4()),
        }

    if not data.get("idempotency_key"):
        data["idempotency_key"] = str(uuid.uuid4())
    if not data.get("base_url"):
        data["base_url"] = resolve_base_url()
    return data


def _endpoint_url(base_url: str, endpoint: str) -> str:
    path = ENDPOINT_PATHS.get(endpoint)
    if not path:
        raise ValueError(f"Unknown endpoint: {endpoint}")
    return f"{base_url.rstrip('/')}{path}"


def _post_envelope(url: str, payload: Dict[str, Any], headers: Dict[str, str]) -> Any:
    return post_with_backoff(url, payload, headers)


def _deliver_monitoring_payload(
    url: str,
    payload: Dict[str, Any],
    *,
    api_key: str,
    idempotency_key: str,
) -> Dict[str, Any]:
    """Deliver a monitoring payload, registering first when run_id is missing.

    An empty run_id means Seer was unreachable at job start, so the offline
    final was queued without a server-assigned id. Replay must register a run,
    then post the terminal status/logs with that run_id.
    """
    body = dict(payload)
    run_id = body.get("run_id") or ""

    if not run_id:
        register_payload = {
            "job_name": body.get("job_name"),
            "status": "running",
            "run_id": "",
            "start_time": body.get("start_time"),
            "end_time": None,
            "metadata": body.get("metadata"),
            "error_details": None,
            "tags": body.get("tags"),
            "logs": None,
        }
        register_headers = {
            "Authorization": api_key,
            "Content-Type": "application/json",
            "Idempotency-Key": f"{idempotency_key}:register",
        }
        response = _post_envelope(url, register_payload, register_headers)
        registered = parse_json_response(response)
        run_id = registered.get("run_id") or ""
        if not run_id:
            raise RuntimeError(
                "Seer register succeeded but returned no run_id during offline replay"
            )
        body["run_id"] = run_id

    complete_headers = {
        "Authorization": api_key,
        "Content-Type": "application/json",
        "Idempotency-Key": f"{idempotency_key}:complete",
    }
    _post_envelope(url, body, complete_headers)
    return body


def _deliver_envelope(
    endpoint: str,
    url: str,
    payload: Dict[str, Any],
    *,
    api_key: str,
    idempotency_key: str,
) -> Optional[Dict[str, Any]]:
    if endpoint == "monitoring":
        return _deliver_monitoring_payload(
            url,
            payload,
            api_key=api_key,
            idempotency_key=idempotency_key,
        )

    headers = {
        "Authorization": api_key,
        "Content-Type": "application/json",
        "Idempotency-Key": idempotency_key,
    }
    _post_envelope(url, payload, headers)
    return None


@dataclass
class ReplayResult:
    sent: int = 0
    failed: int = 0
    dead_lettered: int = 0
    skipped: bool = False
    errors: Optional[List[str]] = None

    def __post_init__(self) -> None:
        if self.errors is None:
            self.errors = []


def replay_failed_payloads(
    api_key: str,
    *,
    base_url: Optional[str] = None,
    queue_dir: Optional[str] = None,
    max_attempts: int = DEFAULT_MAX_ATTEMPTS,
    lock_timeout: float = 0,
) -> ReplayResult:
    """Replay queued envelopes under a directory lock.

    Uses FileLock so only one process replays at a time. Individual files are
    claimed via rename to ``*.sending`` before POST to avoid double-sends.
    Each envelope's ``idempotency_key`` is sent as the ``Idempotency-Key`` header.
    Replay targets ``envelope["base_url"]`` when present so queued events stay
    pinned to the host they were originally intended for.
    """
    result = ReplayResult()
    path = _ensure_queue_dir(queue_dir)
    fallback_base = resolve_base_url(base_url)
    lock = FileLock(os.path.join(path, ".replay.lock"), timeout=lock_timeout)

    try:
        lock.acquire(timeout=lock_timeout)
    except Timeout:
        result.skipped = True
        print("Seer queue replay already in progress; skipping.")
        return result

    try:
        files = _list_queue_files(path)

        for filename in files:
            filepath = os.path.join(path, filename)
            claimed = f"{filepath}.sending"
            try:
                os.rename(filepath, claimed)
            except OSError:
                # Another writer/replayer moved it; skip.
                continue

            try:
                envelope = _load_envelope(claimed)
                endpoint = envelope["endpoint"]
                target_base = envelope.get("base_url") or fallback_base
                envelope["base_url"] = target_base
                url = _endpoint_url(target_base, endpoint)
                idem_key = envelope.get("idempotency_key") or str(uuid.uuid4())
                envelope["idempotency_key"] = idem_key
                delivered = _deliver_envelope(
                    endpoint,
                    url,
                    envelope["payload"],
                    api_key=api_key,
                    idempotency_key=idem_key,
                )
                # Persist assigned run_id back onto the in-memory envelope for debugging;
                # file is deleted on success anyway.
                if isinstance(delivered, dict) and delivered.get("run_id"):
                    envelope["payload"] = delivered
                os.remove(claimed)
                result.sent += 1
                print(f"Successfully replayed {endpoint} event to SEER")
            except Exception as exc:
                envelope = _safe_load_for_retry(claimed)
                envelope["attempts"] = int(envelope.get("attempts", 0)) + 1
                if not envelope.get("idempotency_key"):
                    envelope["idempotency_key"] = str(uuid.uuid4())
                if not envelope.get("base_url"):
                    envelope["base_url"] = fallback_base
                if envelope["attempts"] >= max_attempts:
                    dead_path = os.path.join(
                        path, "dead", os.path.basename(filepath)
                    )
                    _atomic_write_json(dead_path, envelope)
                    try:
                        os.remove(claimed)
                    except OSError:
                        pass
                    result.dead_lettered += 1
                    msg = f"Moved to dead letter after {max_attempts} attempts: {filename}"
                    result.errors.append(msg)
                    print(msg)
                else:
                    _atomic_write_json(filepath, envelope)
                    try:
                        os.remove(claimed)
                    except OSError:
                        pass
                    result.failed += 1
                    msg = f"Unable to send payload ({filename}): {exc}"
                    result.errors.append(msg)
                    print(msg)
    finally:
        lock.release()

    return result


def _safe_load_for_retry(claimed_path: str) -> Dict[str, Any]:
    try:
        return _load_envelope(claimed_path)
    except Exception:
        return {
            "version": ENVELOPE_VERSION,
            "endpoint": "monitoring",
            "base_url": resolve_base_url(),
            "payload": {},
            "created_at": _utc_now_iso(),
            "attempts": 0,
            "idempotency_key": str(uuid.uuid4()),
        }
