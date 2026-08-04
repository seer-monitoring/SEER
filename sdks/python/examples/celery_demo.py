"""Minimal Celery + Seer demo (eager mode).

Usage:
  pip install -e ".[celery,dev]"
  python examples/celery_demo.py
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from celery import Celery

from seerpy import Seer
from seerpy.integrations.celery import SeerTask, set_default_seer

api_key = os.getenv("SEER_API_KEY", "dev-key")
base_url = os.getenv("SEER_BASE_URL")  # optional

seer = Seer(api_key=api_key, base_url=base_url, auto_replay=False)
set_default_seer(seer)

app = Celery("seer_demo")
app.conf.task_always_eager = True
app.conf.task_eager_propagates = True


@app.task(base=SeerTask, seer_capture_logs=True, name="demo.hello")
def hello(name: str = "world") -> str:
    msg = f"hello, {name}"
    print(msg)
    return msg


if __name__ == "__main__":
    print(hello.delay("seer").get())
