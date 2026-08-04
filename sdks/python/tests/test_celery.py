"""Tests for Celery SeerTask integration."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

import pytest

celery = pytest.importorskip("celery")

from celery import Celery

from seerpy import Seer
from seerpy.integrations.celery import SeerTask, set_default_seer


@pytest.fixture
def eager_app():
    app = Celery("seer_test")
    app.conf.task_always_eager = True
    app.conf.task_eager_propagates = True
    return app


def test_seer_task_wraps_monitor(eager_app, monkeypatch):
    monkeypatch.setenv("SEER_REPLAY_JITTER_MS", "0")
    seer = Seer(api_key="test-key", auto_replay=False)
    set_default_seer(seer)

    @eager_app.task(base=SeerTask, name="tests.add", seer_capture_logs=False)
    def add(a, b):
        return a + b

    with patch.object(Seer, "monitor") as mock_monitor:
        mock_monitor.return_value.__enter__ = MagicMock(return_value=None)
        mock_monitor.return_value.__exit__ = MagicMock(return_value=False)
        result = add.delay(2, 3).get()
        assert result == 5
        mock_monitor.assert_called()
        args, kwargs = mock_monitor.call_args
        assert args[0] == "tests.add"
