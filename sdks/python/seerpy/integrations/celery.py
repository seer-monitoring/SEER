"""Celery integration for Seer.

Install with::

    pip install seerpy[celery]

Use ``SeerTask`` as a base class, or ``connect_seer_signals`` for app-wide hooks.
"""

from __future__ import annotations

from typing import Any, Dict, Optional

from seerpy.seer import Seer

try:
    from celery import Task
    from celery.signals import task_failure, task_postrun, task_prerun
except ImportError as exc:  # pragma: no cover - exercised when celery extra missing
    raise ImportError(
        "Celery support requires the celery package. Install with: pip install seerpy[celery]"
    ) from exc


_DEFAULT_SEER: Optional[Seer] = None
_RUN_IDS: Dict[str, str] = {}
_SIGNAL_SEER: Optional[Seer] = None
_SIGNALS_CONNECTED = False


def set_default_seer(seer: Seer) -> None:
    """Set the Seer client used when a task does not define ``seer``."""
    global _DEFAULT_SEER
    _DEFAULT_SEER = seer


def get_default_seer() -> Optional[Seer]:
    return _DEFAULT_SEER


class SeerTask(Task):
    """Celery Task base that wraps execution in ``seer.monitor``.

    Attributes you can set on the task class or instance:

    - ``seer``: ``Seer`` client (falls back to ``set_default_seer``)
    - ``seer_job_name``: override job name (default: Celery task name)
    - ``seer_capture_logs``: capture stdout/logging into the run
    - ``seer_metadata`` / ``seer_tags``: optional static metadata/tags
    """

    abstract = True
    seer: Optional[Seer] = None
    seer_job_name: Optional[str] = None
    seer_capture_logs: bool = False
    seer_metadata: Optional[dict] = None
    seer_tags: Optional[list] = None

    def __call__(self, *args: Any, **kwargs: Any) -> Any:
        client = self.seer or get_default_seer()
        if client is None:
            return super().__call__(*args, **kwargs)

        job_name = self.seer_job_name or self.name
        with client.monitor(
            job_name,
            capture_logs=self.seer_capture_logs,
            metadata=self.seer_metadata,
            tags=self.seer_tags,
        ):
            return super().__call__(*args, **kwargs)


def connect_seer_signals(seer: Seer, *, app=None) -> None:
    """Connect Celery signals so every task is monitored.

    Prefer ``SeerTask`` when you want per-task control. Signals are a convenient
    app-wide alternative. Pass ``app`` only for documentation/clarity; Celery
    signals are process-global.
    """
    global _SIGNAL_SEER, _SIGNALS_CONNECTED
    _SIGNAL_SEER = seer
    if _SIGNALS_CONNECTED:
        return

    @task_prerun.connect(weak=False)
    def _on_prerun(task_id=None, task=None, **_kwargs):
        client = _SIGNAL_SEER
        if client is None or task is None:
            return
        job_name = getattr(task, "seer_job_name", None) or getattr(task, "name", "celery_task")
        start_payload = {
            "job_name": job_name,
            "status": "running",
            "run_id": "",
            "start_time": None,
            "end_time": None,
            "metadata": {"celery_task_id": task_id},
            "error_details": None,
            "tags": ["celery"],
            "logs": None,
        }
        from datetime import datetime, timezone

        start_payload["start_time"] = datetime.now(timezone.utc).isoformat(sep=" ")
        try:
            from seerpy.http import parse_json_response

            resp = client._post("/monitoring", start_payload)
            data = parse_json_response(resp)
            run_id = data.get("run_id")
            if run_id and task_id:
                _RUN_IDS[task_id] = run_id
        except Exception:
            # Offline: leave no run_id; postrun/failure will queue final outcome.
            pass

    @task_postrun.connect(weak=False)
    def _on_postrun(task_id=None, task=None, state=None, **_kwargs):
        client = _SIGNAL_SEER
        if client is None or task is None or state == "FAILURE":
            return
        _complete_signal_run(client, task_id, task, status="success", error=None)

    @task_failure.connect(weak=False)
    def _on_failure(task_id=None, exception=None, sender=None, **_kwargs):
        client = _SIGNAL_SEER
        if client is None:
            return
        task = sender
        err = str(exception) if exception is not None else "task failed"
        _complete_signal_run(client, task_id, task, status="failed", error=err)

    _SIGNALS_CONNECTED = True


def _complete_signal_run(client: Seer, task_id, task, *, status: str, error: Optional[str]) -> None:
    from datetime import datetime, timezone

    job_name = "celery_task"
    if task is not None:
        job_name = getattr(task, "seer_job_name", None) or getattr(task, "name", job_name)
    run_id = _RUN_IDS.pop(task_id, "") if task_id else ""
    end_time = datetime.now(timezone.utc).isoformat(sep=" ")
    payload = {
        "job_name": job_name,
        "status": status,
        "run_id": run_id or "",
        "start_time": None,
        "end_time": end_time,
        "metadata": {"celery_task_id": task_id},
        "error_details": error,
        "tags": ["celery"],
        "logs": None,
    }
    try:
        client._post("/monitoring", payload)
    except Exception:
        from seerpy.payloads import save_failed_payload

        save_failed_payload(payload, "monitoring", base_url=client.base_url)
