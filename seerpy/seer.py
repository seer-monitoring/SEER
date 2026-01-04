from contextlib import contextmanager
import time, traceback, requests
import logging
from io import StringIO
from .payloads import save_failed_payload,replay_failed_payloads
from datetime import datetime,timezone
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


class Seer:
    def __init__(self,apiKey: str):
        self.api_key = apiKey

    def post_with_backoff(self,url, payload,headers, max_retries=5, base_delay=1, max_delay=30):
        for attempt in range(max_retries):
            try:
                response = requests.post(url,headers=headers, json=payload,allow_redirects=False,timeout=100)
                req = response.request
                try:
                    response.raise_for_status()
                    return response
                    
                except requests.exceptions.HTTPError as e:
                    # Include the response text in the exception message
                    print("X Failed Connecting to SEER:\n")
                    print(f"{e}\nResponse body:\n{response.text}")
                    raise
            except Exception as e:
                if attempt == max_retries - 1:
                    print("X Error Connecting to SEER. Continuing without SEER Monitoring. Please Check https://status.seer.ansrstudio.com")
                    raise
                delay = min(base_delay * (2 ** attempt), max_delay)
                time.sleep(delay)


    @contextmanager
    def monitor(self,job_name, capture_logs=False,metadata:dict =None):
        start_time = datetime.now(timezone.utc).isoformat(sep=' ')
        status = "success"
        error = None
        log_stream = None
        log_contents = None
        handler = None
        run_id = None
        monitoring_payload_saved = False
        seer_ready = True
        payload={
            "job_name": job_name,
            "status": "running",
            "run_id": "",
            "start_time": start_time,
            "end_time": None ,
            "metadata": metadata,
            "error_details": error,
            "tags": None,
            "logs": log_contents
        }
        headers = {
            "Authorization": getattr(self, "api_key", None),
            "Content-Type": "application/json"
        }
        try:
            id_response = self.post_with_backoff("https://api.ansrstudio.com/monitoring", payload,headers, max_retries=5, base_delay=1, max_delay=30)
            id_response_dict = json.loads(id_response.json())
            run_id = id_response_dict.get("run_id")
            if seer_ready:
                print('✓ Connected to SEER monitoring') 
                print(f'✓ Pipeline "{job_name}" registered')
        except Exception as e:
            print(e)
            save_failed_payload(payload, "monitoring") 
            monitoring_payload_saved = True
            seer_ready = False
        if capture_logs:
            log_stream = StringIO()
            original_stdout = sys.stdout
            sys.stdout = StreamTee(sys.stdout, log_stream)

            # Hook up logging to the same buffer
            handler = logging.StreamHandler(log_stream)
            logger = logging.getLogger()
            logger.setLevel(logging.DEBUG)
            logger.handlers = []
            logger.addHandler(handler)
            if seer_ready:
                print('✓ Capturing Logs')
        try:
            if seer_ready:
                print('→ Monitoring active.') 
            print('Starting Code...')
            yield  # This is where the user's code runs
        except Exception as e:
            status = "failed"
            error = traceback.format_exc()
            raise  # re-raises the error so the script fails visibly
        finally:
            if capture_logs:
                sys.stdout = original_stdout
            end_time = datetime.now(timezone.utc).isoformat(sep=' ')
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
                    "tags": None,
                    "logs": log_contents
                }
                try:
                    self.post_with_backoff("https://api.ansrstudio.com/monitoring", payload,headers, max_retries=5, base_delay=1, max_delay=30)
                    print('✓ Monitoring complete.')
                except Exception as e:
                    save_failed_payload(payload, "monitoring")
                    raise 
            else:
                if not monitoring_payload_saved:
                    save_failed_payload(payload, "monitoring") 
                print("Seer unable to start.")            

    def heartbeat(self,job_name,metadata=None):
        current_time = datetime.now(timezone.utc).isoformat(sep=' ')
        payload={
            "job_name": job_name,
            "current_time": current_time,
            "metadata": metadata
        }
        headers = {
            "Authorization": getattr(self, "api_key", None),
            "Content-Type": "application/json"
        }
        try:
            self.post_with_backoff("https://api.ansrstudio.com/heartbeat", payload,headers, max_retries=5, base_delay=1, max_delay=30)
            print('Heartbeat recived')
        except Exception as e:
            save_failed_payload(payload, "heartbeat")
	