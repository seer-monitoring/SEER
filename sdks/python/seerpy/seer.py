"""Seer monitoring client."""

from __future__ import annotations

import atexit
import logging
import sys
import threading
import time
import traceback
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from io import StringIO
from typing import Any, Dict, Iterator, List, Optional

import requests

from .http import parse_json_response, post_with_backoff, replay_startup_jitter_seconds
from .payloads import (
    ReplayResult,
    replay_failed_payloads,
    resolve_base_url,
    save_failed_payload,
)

DEFAULT_REPLAY_INTERVAL = 60.0


class StreamTee:
    """Writes to both the original stream and a buffer (like StringIO)."""

    def __init__(self, original, copy_to):
        self.original = original
        self.copy_to = copy_to

    def write(self, message):
        self.original.write(message)
        self.copy_to.write(message)

    def flush(self):
        self.original.flush()
        self.copy_to.flush()

    def isatty(self):
        return getattr(self.original, "isatty", lambda: False)()

    def fileno(self):
        return self.original.fileno()

    @property
    def encoding(self):
        return getattr(self.original, "encoding", None)


class Seer:
    def __init__(
        self,
        apiKey: Optional[str] = None,
        *,
        api_key: Optional[str] = None,
        auto_replay: bool = False,
        background_replay: bool = False,
        replay_interval: float = DEFAULT_REPLAY_INTERVAL,
        base_url: Optional[str] = None,
        timeout: float = 30,
    ):
        key = api_key or apiKey
        if not key:
            raise ValueError("API key is required (api_key or apiKey)")
        if replay_interval <= 0:
            raise ValueError("replay_interval must be > 0")

        self.api_key = key
        self.base_url = resolve_base_url(base_url)
        self.timeout = timeout
        self.replay_interval = float(replay_interval)
        self._session = requests.Session()
        self._bg_stop = threading.Event()
        self._bg_thread: Optional[threading.Thread] = None
        self._atexit_registered = False

        if auto_replay:
            try:
                jitter = replay_startup_jitter_seconds()
                if jitter > 0:
                    time.sleep(jitter)
                self.replay()
            except Exception as exc:
                print(f"Seer auto_replay skipped: {exc}")

        if background_replay:
            self.start_background_replay()

    def _headers(self, *, idempotency_key: Optional[str] = None) -> Dict[str, str]:
        headers = {
            "Authorization": self.api_key,
            "Content-Type": "application/json",
        }
        if idempotency_key:
            headers["Idempotency-Key"] = idempotency_key
        return headers

    def _url(self, path: str) -> str:
        return f"{self.base_url}{path}"

    def _post(
        self,
        path: str,
        payload: Dict[str, Any],
        *,
        idempotency_key: Optional[str] = None,
    ):
        key = idempotency_key or str(uuid.uuid4())
        return post_with_backoff(
            self._url(path),
            payload,
            self._headers(idempotency_key=key),
            timeout=self.timeout,
            session=self._session,
        )

    def replay(self, *, max_attempts: int = 5) -> ReplayResult:
        """Flush the local offline queue to SEER."""
        return replay_failed_payloads(
            self.api_key,
            base_url=self.base_url,
            max_attempts=max_attempts,
        )

    def start_background_replay(self) -> None:
        """Start a daemon thread that periodically flushes the offline queue."""
        if self._bg_thread is not None and self._bg_thread.is_alive():
            return

        self._bg_stop.clear()

        def _loop() -> None:
            # Stampede guard before the first flush when many workers start together.
            jitter = replay_startup_jitter_seconds()
            if jitter > 0 and self._bg_stop.wait(timeout=jitter):
                return
            while not self._bg_stop.is_set():
                try:
                    self.replay()
                except Exception as exc:
                    print(f"Seer background_replay error: {exc}")
                if self._bg_stop.wait(timeout=self.replay_interval):
                    break

        self._bg_thread = threading.Thread(
            target=_loop,
            name="seer-background-replay",
            daemon=True,
        )
        self._bg_thread.start()
        if not self._atexit_registered:
            atexit.register(self.stop_background_replay)
            self._atexit_registered = True

    def stop_background_replay(self, timeout: float = 2.0) -> None:
        """Stop the background flusher if it is running."""
        self._bg_stop.set()
        thread = self._bg_thread
        if thread is not None and thread.is_alive() and thread is not threading.current_thread():
            thread.join(timeout=timeout)
        self._bg_thread = None

    @contextmanager
    def monitor(
        self,
        job_name: str,
        capture_logs: bool = False,
        metadata: Optional[dict] = None,
        tags: Optional[List[str]] = None,
    ) -> Iterator[None]:
        start_time = datetime.now(timezone.utc).isoformat(sep=" ")
        status = "success"
        error = None
        log_stream = None
        log_contents = None
        handler = None
        run_id = None
        original_stdout = None
        logger = None
        previous_level = None
        user_failed = False

        start_payload = {
            "job_name": job_name,
            "status": "running",
            "run_id": "",
            "start_time": start_time,
            "end_time": None,
            "metadata": metadata,
            "error_details": None,
            "tags": tags,
            "logs": None,
        }

        try:
            id_response = self._post("/monitoring", start_payload)
            id_response_dict = parse_json_response(id_response)
            run_id = id_response_dict.get("run_id")
            print("✓ Connected to SEER monitoring")
            print(f'✓ Pipeline "{job_name}" registered')
        except Exception as exc:
            # Do not queue the running stub; we persist the final outcome below.
            print(exc)
            print("Seer unavailable at start; will queue final result if needed.")

        if capture_logs:
            log_stream = StringIO()
            original_stdout = sys.stdout
            sys.stdout = StreamTee(sys.stdout, log_stream)

            handler = logging.StreamHandler(log_stream)
            logger = logging.getLogger()
            previous_level = logger.level
            logger.setLevel(logging.DEBUG)
            logger.addHandler(handler)
            if run_id:
                print("✓ Capturing Logs")

        try:
            if run_id:
                print("→ Monitoring active.")
            print("Starting Code...")
            yield
        except Exception:
            status = "failed"
            error = traceback.format_exc()
            user_failed = True
            raise
        finally:
            if capture_logs and original_stdout is not None:
                sys.stdout = original_stdout
            if capture_logs and handler is not None and logger is not None:
                handler.flush()
                logger.removeHandler(handler)
                handler.close()
                if previous_level is not None:
                    logger.setLevel(previous_level)
                if log_stream is not None:
                    log_contents = log_stream.getvalue()

            end_time = datetime.now(timezone.utc).isoformat(sep=" ")
            final_payload = {
                "job_name": job_name,
                "status": status,
                "run_id": run_id or "",
                "start_time": start_time,
                "end_time": end_time,
                "metadata": metadata,
                "error_details": error,
                "tags": tags,
                "logs": log_contents,
            }

            if run_id:
                idem_key = str(uuid.uuid4())
                try:
                    self._post("/monitoring", final_payload, idempotency_key=idem_key)
                    print("✓ Monitoring complete.")
                except Exception as exc:
                    save_failed_payload(
                        final_payload,
                        "monitoring",
                        idempotency_key=idem_key,
                        base_url=self.base_url,
                    )
                    # Never raise from finally — that would mask a user exception
                    # and should not fail the job because monitoring is down.
                    if not user_failed:
                        print(f"Seer completion upload failed; queued for replay: {exc}")
            else:
                save_failed_payload(
                    final_payload,
                    "monitoring",
                    idempotency_key=str(uuid.uuid4()),
                    base_url=self.base_url,
                )
                print("Seer unable to start; final result queued for replay.")

    def heartbeat(
        self,
        job_name: str,
        metadata: Optional[dict] = None,
        tags: Optional[List[str]] = None,
    ) -> None:
        current_time = datetime.now(timezone.utc).isoformat(sep=" ")
        payload = {
            "job_name": job_name,
            "current_time": current_time,
            "metadata": metadata,
            "tags": tags,
        }
        idem_key = str(uuid.uuid4())
        try:
            self._post("/heartbeat", payload, idempotency_key=idem_key)
            print("Heartbeat received")
        except Exception:
            save_failed_payload(
                payload,
                "heartbeat",
                idempotency_key=idem_key,
                base_url=self.base_url,
            )
