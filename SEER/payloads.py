import os
import json
import requests
from datetime import datetime

def save_failed_payload(payload, endpoint):
    temp_dir = os.path.join(os.path.dirname(__file__), "failed_payloads")
    os.makedirs(temp_dir, exist_ok=True)
    timestamp = datetime.utcnow().strftime("%Y%m%d%H%M%S%f")
    filename = f"{endpoint.replace('/', '_')}_{timestamp}.json"
    filepath = os.path.join(temp_dir, filename)
    with open(filepath, "w") as f:
        json.dump(payload, f)

def replay_failed_payloads():
    temp_dir = os.path.join(os.path.dirname(__file__), "failed_payloads")
    if not os.path.exists(temp_dir):
        return
    for filename in os.listdir(temp_dir):
        filepath = os.path.join(temp_dir, filename)
        with open(filepath, "r") as f:
            payload = json.load(f)
        if "monitoring" in filename:
            url = "https://api.ansrstudio.com/monitoring"
        elif "heartbeat" in filename:
            url = "https://api.ansrstudio.com/heartbeat"
        else:
            continue
        try:
            requests.post(url, json=payload)
            os.remove(filepath)
        except Exception:
            continue  # Leave the file for next retry