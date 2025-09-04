from contextlib import contextmanager
import time, traceback, requests
import logging
from io import StringIO
from payloads import save_failed_payload,replay_failed_payloads
from datetime import datetime
import json
import sys

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

class Config:
   api_key = None

class Seer:
    def __init__(self,apiKey: str):
        self.api_key = apiKey

    def post_with_backoff(self,url, payload,headers, max_retries=5, base_delay=1, max_delay=30):
        for attempt in range(max_retries):
            try:
                response = requests.post(url,headers=headers, json=payload,allow_redirects=False)
                req = response.request

                # print("Request method:", req.method)
                # print("Request URL:", req.url)
                # print("Request headers:", req.headers)
                # print("Request body:", req.body)
                response.raise_for_status()
                return response
            except Exception as e:
                if attempt == max_retries - 1:
                    raise  # Give up after max_retries
                delay = min(base_delay * (2 ** attempt), max_delay)
                time.sleep(delay)


    @contextmanager
    def monitor(self,job_name, capture_logs=False,tags=None,metadata:dict =None):
        start_time = datetime.now().isoformat(sep=' ')
        status = "success"
        error = None
        log_stream = None
        log_contents = None
        handler = None
        run_id = None
        payload={
            "job_name": job_name,
            "status": "running",
            "run_id": "",
            "start_time": start_time,
            "end_time": None ,
            "metadata": metadata,
            "error_details": error,
            "tags": tags,
            "logs": log_contents
        }
        headers = {
            "auth": getattr(self, "api_key", None),
            "Content-Type": "application/json"
        }
        try:
            id_response = self.post_with_backoff("https://api.seer.ansrstudio.com/monitoring", payload,headers, max_retries=5, base_delay=1, max_delay=30)
            id_response_dict = json.loads(id_response.json())
            run_id = id_response_dict.get("run_id")
        except Exception as e:
            print(e)
            save_failed_payload(payload, "monitoring") 
        if capture_logs:
            log_stream = StringIO()
            sys.stdout = StreamTee(sys.stdout, log_stream)

            # Hook up logging to the same buffer
            handler = logging.StreamHandler(log_stream)
            logger = logging.getLogger()
            logger.setLevel(logging.DEBUG)
            logger.addHandler(handler)
        try:
            yield  # This is where the user's code runs
            status = 'success'
        except Exception as e:
            status = "failed"
            error = traceback.format_exc()
            raise  # re-raises the error so the script fails visibly
        finally:
            end_time = datetime.now().isoformat(sep=' ')
            if capture_logs and handler:
                handler.flush()
                logger.removeHandler(handler)
                log_contents = log_stream.getvalue()
            if run_id:
                payload={
                    "job_name": job_name,
                    "status": status,
                    "run_id": run_id,
                    "start_time": start_time,
                    "end_time": end_time ,
                    "metadata": metadata,
                    "error_details": error,
                    "tags": tags,
                    "logs": log_contents
                }
                try:
                    self.post_with_backoff("https://api.seer.ansrstudio.com/monitoring", payload,headers, max_retries=5, base_delay=1, max_delay=30)
                except Exception as e:
                    save_failed_payload(payload, "monitoring")
                    raise 
            else:
                print("Seer unable to start.")            

    @contextmanager
    def heartbeat(self,job_name,metadata=None):
        current_time = datetime.now().isoformat(sep=' ')
        payload={
            "app": "seer",
            "job_name": job_name,
            "current_time": current_time,
            "metadata": metadata
        }
        headers = {
            "auth": getattr(self, "api_key", None),
            "Content-Type": "application/json"
        }
        try:
            self.post_with_backoff("https://api.seer.ansrstudio.com/heartbeat", payload,headers, max_retries=5, base_delay=1, max_delay=30)
        except Exception as e:
            save_failed_payload(payload, "heartbeat")
            raise