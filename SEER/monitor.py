from contextlib import contextmanager
import time, traceback, requests
import logging
from io import StringIO
from payloads import save_failed_payload
class Config:
   api_key = None

def init(apikey: str):
    Config.api_key = apikey

def post_with_backoff(url, payload, max_retries=5, base_delay=1, max_delay=30):
    for attempt in range(max_retries):
        try:
            response = requests.post(url, json=payload)
            response.raise_for_status()
            return response
        except Exception as e:
            if attempt == max_retries - 1:
                raise  # Give up after max_retries
            delay = min(base_delay * (2 ** attempt), max_delay)
            time.sleep(delay)


@contextmanager
def monitor(job_name, capture_logs=False,tags=None):
    start_time = time.time()
    status = "success"
    error = None
    log_stream = None
    log_contents = None
    handler = None

    if capture_logs:
        log_stream = StringIO()
        handler = logging.StreamHandler(log_stream)
        logger = logging.getLogger()
        logger.addHandler(handler)

    try:
        yield  # This is where the user's code runs
    except Exception as e:
        status = "error"
        error = traceback.format_exc()
        raise  # re-raises the error so the script fails visibly
    finally:
        end_time = time.time()
        if capture_logs and handler:
            handler.flush()
            logger.removeHandler(handler)
            log_contents = log_stream.getvalue()
        payload={
                "app": "seer",
                "job_name": job_name,
                "status": status,
                "duration": end_time - start_time,
                "error": error,
                "tags": tags,
                "logs": log_contents,
                "timestamp": start_time,
                "api_key": getattr(Config, "api_key", None)
            }
        try:
            post_with_backoff("https://api.ansrstudio.com/monitoring", payload, max_retries=5, base_delay=1, max_delay=30)
        except Exception:
            save_failed_payload(payload, "monitoring")


def heartbeat(job_name):
    current_time = time.time()
    payload={
        "app": "seer",
        "job_name": job_name,
        "current_time": current_time,
        "api_key": getattr(Config, "api_key", None)
    }
    try:
        post_with_backoff("https://api.ansrstudio.com/heartbeat", payload, max_retries=5, base_delay=1, max_delay=30)
    except Exception:
        save_failed_payload(payload, "heartbeat")