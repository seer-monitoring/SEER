import os
import json
import requests
import time
from datetime import datetime

def post_with_backoff(url, payload,headers, max_retries=5, base_delay=1, max_delay=30):
    for attempt in range(max_retries):
        try:
            response = requests.post(url,headers=headers, json=payload,allow_redirects=False,timeout=100)
            req = response.request
            try:
                response.raise_for_status()
            except requests.exceptions.HTTPError as e:
                # Include the response text in the exception message
                raise requests.exceptions.HTTPError(
                    f"{e}\nResponse body:\n{response.text}"
                ) from e
            return response
        except Exception as e:
            if attempt == max_retries - 1:
                raise  # Give up after max_retries
            delay = min(base_delay * (2 ** attempt), max_delay)
            time.sleep(delay)

def save_failed_payload(payload, endpoint):
    temp_dir = os.path.join(os.path.dirname(__file__), "failed_payloads")
    os.makedirs(temp_dir, exist_ok=True)
    timestamp = datetime.utcnow().strftime("%Y%m%d%H%M%S%f")
    filename = f"{endpoint.replace('/', '_')}_{timestamp}.json"
    filepath = os.path.join(temp_dir, filename)
    with open(filepath, "w") as f:
        json.dump(payload, f)
    print(f"Seer upload failed, saving to {filepath}")
    print("Call replay_failed_payloads to retrigger events.")

def replay_failed_payloads(api_key: str):
    temp_dir = os.path.join(os.path.dirname(__file__), "failed_payloads")
    if not os.path.exists(temp_dir):
        return
    for filename in os.listdir(temp_dir):
        if filename.endswith("json"):
            filepath = os.path.join(temp_dir, filename)
            print(f"Reading: {filepath}")
            with open(filepath, "r") as f:
                payload = json.load(f)
            headers = {
                "Authorization": api_key,
                "Content-Type": "application/json"
            }
            if "monitoring" in filename:
                url = "https://api.ansrstudio.com/monitoring"
            elif "heartbeat" in filename:
                url = "https://api.ansrstudio.com/heartbeat"
            else:
                continue
            try:
                post_with_backoff(url, payload,headers)
                print(f"Successfully sent {payload} to SEER")
                os.remove(filepath)
            except Exception:
                print("Unable to send payload to SEER.")
                continue  # Leave the file for next retry