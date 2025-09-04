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
    print(f"Seer upload failed, saving to {filepath}")
    print("Call replay_failed_payloads to retrigger events.")

def replay_failed_payloads(api_key: str):
    temp_dir = os.path.join(os.path.dirname(__file__), "failed_payloads")
    if not os.path.exists(temp_dir):
        return
    for filename in os.listdir(temp_dir):
        filepath = os.path.join(temp_dir, filename)
        with open(filepath, "r") as f:
            payload = json.load(f)
        headers = {
            "auth": api_key,
            "Content-Type": "application/json"
        }
        if "monitoring" in filename:
            url = "https://api.seer.ansrstudio.com/monitoring"
        elif "heartbeat" in filename:
            url = "https://api.seer.ansrstudio.com/heartbeat"
        else:
            continue
        try:
            requests.post(url,headers=headers, json=payload)
            os.remove(filepath)
        except Exception:
            continue  # Leave the file for next retry