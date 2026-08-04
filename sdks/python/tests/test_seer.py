"""Tests for Seer HTTP helpers, monitor lifecycle, and offline queue."""

from __future__ import annotations

import json
import logging
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
import requests

from seerpy import Seer
from seerpy.http import compute_backoff_delay, parse_json_response, post_with_backoff
from seerpy.payloads import (
    DEFAULT_BASE_URL,
    queue_status,
    replay_failed_payloads,
    retry_dead,
    save_failed_payload,
)


@pytest.fixture
def queue_dir(tmp_path, monkeypatch):
    path = tmp_path / "queue"
    path.mkdir()
    monkeypatch.setenv("SEER_QUEUE_DIR", str(path))
    return path


def _mock_response(status_code=200, payload=None, text="", headers=None):
    response = MagicMock(spec=requests.Response)
    response.status_code = status_code
    response.text = text or json.dumps(payload or {})
    response.json.return_value = payload if payload is not None else {}
    response.headers = headers or {}
    if status_code >= 400:
        http_error = requests.exceptions.HTTPError(response=response)
        response.raise_for_status.side_effect = http_error
    else:
        response.raise_for_status.return_value = None
    return response


class TestParseJsonResponse:
    def test_dict_body(self):
        response = _mock_response(payload={"run_id": "abc"})
        assert parse_json_response(response)["run_id"] == "abc"

    def test_string_body(self):
        response = MagicMock()
        response.json.return_value = json.dumps({"run_id": "xyz"})
        assert parse_json_response(response)["run_id"] == "xyz"


class TestPostWithBackoff:
    @patch("seerpy.http.requests.post")
    def test_success(self, mock_post):
        mock_post.return_value = _mock_response(payload={"ok": True})
        result = post_with_backoff("https://example.com/x", {}, {})
        assert result is mock_post.return_value
        assert mock_post.call_count == 1

    @patch("seerpy.http.time.sleep")
    @patch("seerpy.http.requests.post")
    def test_retries_5xx_then_succeeds(self, mock_post, _sleep):
        fail = _mock_response(status_code=503, text="down")
        ok = _mock_response(payload={"ok": True})
        mock_post.side_effect = [fail, ok]
        post_with_backoff("https://example.com/x", {}, {}, max_retries=3)
        assert mock_post.call_count == 2

    @patch("seerpy.http.time.sleep")
    @patch("seerpy.http.requests.post")
    def test_does_not_retry_4xx(self, mock_post, _sleep):
        mock_post.return_value = _mock_response(status_code=401, text="unauthorized")
        with pytest.raises(requests.exceptions.HTTPError):
            post_with_backoff("https://example.com/x", {}, {}, max_retries=5)
        assert mock_post.call_count == 1

    @patch("seerpy.http.time.sleep")
    @patch("seerpy.http.requests.post")
    def test_retries_429(self, mock_post, _sleep):
        limited = _mock_response(status_code=429, text="slow down")
        ok = _mock_response(payload={"ok": True})
        mock_post.side_effect = [limited, ok]
        post_with_backoff("https://example.com/x", {}, {}, max_retries=3)
        assert mock_post.call_count == 2

    def test_full_jitter_bounds(self):
        import random

        rng = random.Random(0)
        for attempt in range(6):
            ceiling = min(1 * (2**attempt), 30)
            for _ in range(30):
                d = compute_backoff_delay(attempt, base_delay=1, max_delay=30, rng=rng)
                assert 0 <= d <= ceiling

    @patch("seerpy.http.time.sleep")
    @patch("seerpy.http.requests.post")
    def test_429_honors_retry_after(self, mock_post, mock_sleep):
        limited = _mock_response(
            status_code=429, text="slow down", headers={"Retry-After": "2.5"}
        )
        ok = _mock_response(payload={"ok": True})
        mock_post.side_effect = [limited, ok]
        post_with_backoff("https://example.com/x", {}, {}, max_retries=3)
        mock_sleep.assert_called()
        assert mock_sleep.call_args_list[0].args[0] == 2.5


class TestMonitor:
    @patch.object(Seer, "_post")
    def test_success_path(self, mock_post):
        start = _mock_response(payload={"run_id": "run-1"})
        finish = _mock_response(payload={"ok": True})
        mock_post.side_effect = [start, finish]

        seer = Seer(api_key="test-key")
        with seer.monitor("job", metadata={"a": 1}, tags=["etl"]):
            pass

        assert mock_post.call_count == 2
        start_payload = mock_post.call_args_list[0].args[1]
        finish_payload = mock_post.call_args_list[1].args[1]
        assert start_payload["status"] == "running"
        assert finish_payload["status"] == "success"
        assert finish_payload["run_id"] == "run-1"
        assert finish_payload["tags"] == ["etl"]

    @patch.object(Seer, "_post")
    def test_user_exception_propagates_and_marks_failed(self, mock_post):
        start = _mock_response(payload={"run_id": "run-2"})
        finish = _mock_response(payload={"ok": True})
        mock_post.side_effect = [start, finish]

        seer = Seer(api_key="test-key")
        with pytest.raises(RuntimeError, match="boom"):
            with seer.monitor("job"):
                raise RuntimeError("boom")

        finish_payload = mock_post.call_args_list[1].args[1]
        assert finish_payload["status"] == "failed"
        assert "RuntimeError: boom" in finish_payload["error_details"]

    @patch.object(Seer, "_post")
    def test_offline_start_queues_final_outcome(self, mock_post, queue_dir):
        mock_post.side_effect = requests.exceptions.ConnectionError("down")

        seer = Seer(api_key="test-key")
        with seer.monitor("offline-job", capture_logs=True):
            print("hello offline")

        files = list(queue_dir.glob("*.json"))
        assert len(files) == 1
        envelope = json.loads(files[0].read_text(encoding="utf-8"))
        assert envelope["endpoint"] == "monitoring"
        assert envelope["payload"]["status"] == "success"
        assert envelope["payload"]["job_name"] == "offline-job"
        assert "hello offline" in (envelope["payload"]["logs"] or "")
        assert envelope["payload"]["run_id"] == ""

    @patch.object(Seer, "_post")
    def test_completion_failure_does_not_mask_user_error(self, mock_post, queue_dir):
        start = _mock_response(payload={"run_id": "run-3"})
        mock_post.side_effect = [
            start,
            requests.exceptions.ConnectionError("down"),
        ]

        seer = Seer(api_key="test-key")
        with pytest.raises(ValueError, match="user-fail"):
            with seer.monitor("job"):
                raise ValueError("user-fail")

        files = list(queue_dir.glob("*.json"))
        assert len(files) == 1
        envelope = json.loads(files[0].read_text(encoding="utf-8"))
        assert envelope["payload"]["status"] == "failed"

    @patch.object(Seer, "_post")
    def test_log_handlers_restored(self, mock_post):
        start = _mock_response(payload={"run_id": "run-4"})
        finish = _mock_response(payload={"ok": True})
        mock_post.side_effect = [start, finish]

        root = logging.getLogger()
        sentinel = logging.StreamHandler()
        root.addHandler(sentinel)
        before = list(root.handlers)

        try:
            seer = Seer(api_key="test-key")
            with seer.monitor("job", capture_logs=True):
                pass
            assert sentinel in root.handlers
            assert len(root.handlers) == len(before)
        finally:
            root.removeHandler(sentinel)


class TestQueueReplay:
    @patch("seerpy.payloads.post_with_backoff")
    def test_replay_success_deletes_file(self, mock_post, queue_dir):
        mock_post.return_value = _mock_response(payload={"ok": True})
        save_failed_payload(
            {"job_name": "j", "status": "success", "run_id": "run-existing"},
            "monitoring",
        )
        assert list(queue_dir.glob("*.json"))

        result = replay_failed_payloads("key")
        assert result.sent == 1
        assert result.failed == 0
        assert not list(queue_dir.glob("*.json"))
        assert mock_post.call_count == 1

    @patch("seerpy.payloads.post_with_backoff")
    def test_replay_offline_final_registers_then_completes(self, mock_post, queue_dir):
        register = _mock_response(payload={"run_id": "assigned-1"})
        complete = _mock_response(payload={"ok": True})
        mock_post.side_effect = [register, complete]

        save_failed_payload(
            {
                "job_name": "j",
                "status": "success",
                "run_id": "",
                "start_time": "2026-01-01 00:00:00+00:00",
                "end_time": "2026-01-01 00:01:00+00:00",
                "logs": "offline logs",
            },
            "monitoring",
            idempotency_key="offline-key",
        )

        result = replay_failed_payloads("key")
        assert result.sent == 1
        assert mock_post.call_count == 2

        register_payload = mock_post.call_args_list[0].args[1]
        complete_payload = mock_post.call_args_list[1].args[1]
        assert register_payload["status"] == "running"
        assert register_payload["run_id"] == ""
        assert complete_payload["status"] == "success"
        assert complete_payload["run_id"] == "assigned-1"
        assert complete_payload["logs"] == "offline logs"

        assert mock_post.call_args_list[0].args[2]["Idempotency-Key"] == "offline-key:register"
        assert mock_post.call_args_list[1].args[2]["Idempotency-Key"] == "offline-key:complete"

    @patch("seerpy.payloads.post_with_backoff")
    def test_replay_sends_idempotency_header(self, mock_post, queue_dir):
        mock_post.return_value = _mock_response(payload={"ok": True})
        path = Path(
            save_failed_payload(
                {"job_name": "j", "status": "success", "run_id": "r1"},
                "monitoring",
                idempotency_key="fixed-key-123",
            )
        )
        envelope = json.loads(path.read_text(encoding="utf-8"))
        assert envelope["idempotency_key"] == "fixed-key-123"

        replay_failed_payloads("key")
        headers = mock_post.call_args.args[2]
        assert headers["Idempotency-Key"] == "fixed-key-123:complete"

    @patch("seerpy.payloads.post_with_backoff")
    def test_replay_failure_keeps_file_and_increments_attempts(self, mock_post, queue_dir):
        mock_post.side_effect = requests.exceptions.ConnectionError("down")
        save_failed_payload(
            {"job_name": "j", "status": "failed", "run_id": "r1"},
            "monitoring",
            idempotency_key="retry-me",
        )

        result = replay_failed_payloads("key", max_attempts=5)
        assert result.failed == 1
        files = list(queue_dir.glob("*.json"))
        assert len(files) == 1
        envelope = json.loads(files[0].read_text(encoding="utf-8"))
        assert envelope["attempts"] == 1
        assert envelope["idempotency_key"] == "retry-me"

    @patch("seerpy.payloads.post_with_backoff")
    def test_dead_letter_after_max_attempts(self, mock_post, queue_dir):
        mock_post.side_effect = requests.exceptions.ConnectionError("down")
        path = Path(save_failed_payload({"job_name": "j"}, "heartbeat"))
        envelope = json.loads(path.read_text(encoding="utf-8"))
        envelope["attempts"] = 4
        path.write_text(json.dumps(envelope), encoding="utf-8")

        result = replay_failed_payloads("key", max_attempts=5)
        assert result.dead_lettered == 1
        assert not list(queue_dir.glob("*.json"))
        assert list((queue_dir / "dead").glob("*.json"))

    @patch("seerpy.payloads.post_with_backoff")
    def test_legacy_raw_payload_still_replays(self, mock_post, queue_dir):
        register = _mock_response(payload={"run_id": "legacy-run"})
        complete = _mock_response(payload={"ok": True})
        mock_post.side_effect = [register, complete]
        legacy = queue_dir / "monitoring_20200101000000.json"
        legacy.write_text(
            json.dumps({"job_name": "legacy", "status": "success", "run_id": ""}),
            encoding="utf-8",
        )

        result = replay_failed_payloads("key")
        assert result.sent == 1
        assert not legacy.exists()
        assert mock_post.call_count == 2

    def test_fifo_eviction_by_max_files(self, queue_dir, monkeypatch):
        monkeypatch.setenv("SEER_QUEUE_MAX_FILES", "2")
        monkeypatch.setenv("SEER_QUEUE_MAX_BYTES", str(10 * 1024 * 1024))

        first = Path(save_failed_payload({"n": 1}, "monitoring", idempotency_key="a"))
        second = Path(save_failed_payload({"n": 2}, "heartbeat", idempotency_key="b"))
        third = Path(save_failed_payload({"n": 3}, "monitoring", idempotency_key="c"))

        remaining = sorted(p.name for p in queue_dir.glob("*.json"))
        assert len(remaining) == 2
        assert not first.exists()
        assert second.exists()
        assert third.exists()

    def test_fifo_eviction_by_max_bytes(self, queue_dir, monkeypatch):
        monkeypatch.setenv("SEER_QUEUE_MAX_FILES", "100")
        monkeypatch.setenv("SEER_QUEUE_MAX_BYTES", "800")

        first = Path(save_failed_payload({"pad": "x" * 200}, "monitoring"))
        second = Path(save_failed_payload({"pad": "y" * 200}, "monitoring"))

        files = list(queue_dir.glob("*.json"))
        assert len(files) == 1
        assert not first.exists()
        assert second.exists()
        assert sum(f.stat().st_size for f in files) <= 800

    @patch.object(Seer, "replay")
    def test_auto_replay_on_init(self, mock_replay, monkeypatch):
        monkeypatch.setenv("SEER_REPLAY_JITTER_MS", "0")
        mock_replay.return_value = MagicMock(sent=0, failed=0)
        Seer(api_key="test-key", auto_replay=True)
        mock_replay.assert_called_once()

    @patch.object(Seer, "replay")
    def test_auto_replay_off_by_default(self, mock_replay):
        Seer(api_key="test-key")
        mock_replay.assert_not_called()


class TestBackgroundReplay:
    @patch.object(Seer, "replay")
    def test_background_replay_flushes_periodically(self, mock_replay, monkeypatch):
        import time

        monkeypatch.setenv("SEER_REPLAY_JITTER_MS", "0")
        mock_replay.return_value = MagicMock(sent=0, failed=0)
        seer = Seer(
            api_key="test-key",
            background_replay=True,
            replay_interval=0.05,
        )
        try:
            deadline = time.time() + 1.0
            while mock_replay.call_count < 2 and time.time() < deadline:
                time.sleep(0.02)
            assert mock_replay.call_count >= 2
            assert seer._bg_thread is not None
            assert seer._bg_thread.daemon is True
        finally:
            seer.stop_background_replay()
            assert seer._bg_thread is None

    @patch.object(Seer, "replay")
    def test_background_replay_off_by_default(self, mock_replay):
        seer = Seer(api_key="test-key")
        assert seer._bg_thread is None
        mock_replay.assert_not_called()

    def test_invalid_replay_interval(self):
        with pytest.raises(ValueError, match="replay_interval"):
            Seer(api_key="test-key", background_replay=True, replay_interval=0)


class TestInit:
    def test_requires_api_key(self):
        with pytest.raises(ValueError):
            Seer()

    def test_apiKey_alias(self):
        seer = Seer(apiKey="legacy-key")
        assert seer.api_key == "legacy-key"

    def test_default_base_url(self, monkeypatch):
        monkeypatch.delenv("SEER_BASE_URL", raising=False)
        seer = Seer(api_key="test-key")
        assert seer.base_url == DEFAULT_BASE_URL.rstrip("/")
        assert seer.base_url == "https://api.ansrstudio.com"

    def test_explicit_base_url(self):
        seer = Seer(api_key="test-key", base_url="https://seer.internal.company.com/")
        assert seer.base_url == "https://seer.internal.company.com"

    def test_base_url_from_env(self, monkeypatch):
        monkeypatch.setenv("SEER_BASE_URL", "https://seer.env.example/")
        seer = Seer(api_key="test-key")
        assert seer.base_url == "https://seer.env.example"

    def test_explicit_base_url_overrides_env(self, monkeypatch):
        monkeypatch.setenv("SEER_BASE_URL", "https://seer.env.example")
        seer = Seer(api_key="test-key", base_url="https://seer.explicit.example")
        assert seer.base_url == "https://seer.explicit.example"


class TestBaseUrlEnvelope:
    @patch("seerpy.payloads.post_with_backoff")
    def test_envelope_stores_base_url(self, mock_post, queue_dir):
        path = Path(
            save_failed_payload(
                {"job_name": "j"},
                "monitoring",
                base_url="https://seer.internal.company.com",
            )
        )
        envelope = json.loads(path.read_text(encoding="utf-8"))
        assert envelope["base_url"] == "https://seer.internal.company.com"

    @patch("seerpy.payloads.post_with_backoff")
    def test_replay_uses_envelope_base_url_not_client(self, mock_post, queue_dir):
        mock_post.return_value = _mock_response(payload={"ok": True})
        save_failed_payload(
            {"job_name": "j", "status": "success", "run_id": "r1"},
            "monitoring",
            base_url="https://original.seer.example",
        )

        replay_failed_payloads("key", base_url="https://new.seer.example")
        assert mock_post.call_args.args[0] == "https://original.seer.example/monitoring"


class TestQueueDiagnostics:
    def test_queue_status_and_retry_dead(self, queue_dir):
        save_failed_payload({"job_name": "live"}, "heartbeat", idempotency_key="p1")
        dead = queue_dir / "dead" / "dead_item.json"
        dead.write_text(
            json.dumps(
                {
                    "version": 3,
                    "endpoint": "monitoring",
                    "base_url": "https://example.com",
                    "payload": {"job_name": "dead_job", "status": "failed"},
                    "created_at": "2026-01-01T00:00:00Z",
                    "attempts": 5,
                    "idempotency_key": "dead-key",
                }
            ),
            encoding="utf-8",
        )
        st = queue_status()
        assert st.pending == 1
        assert st.dead == 1

        result = retry_dead(all_dead=True, flush=False)
        assert result["restored"] == 1
        assert not dead.exists()
        st2 = queue_status()
        assert st2.pending == 2
        assert st2.dead == 0
