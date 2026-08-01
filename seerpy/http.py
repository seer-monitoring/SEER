"""Shared HTTP helpers for the Seer client."""

from __future__ import annotations

import json
import time
from typing import Any, Dict, Optional

import requests

DEFAULT_TIMEOUT = 30
DEFAULT_MAX_RETRIES = 5
DEFAULT_BASE_DELAY = 1
DEFAULT_MAX_DELAY = 30


def _should_retry_status(status_code: Optional[int]) -> bool:
    """Retry on server errors and rate limits; never on other 4xx."""
    if status_code is None:
        return True
    if status_code == 429:
        return True
    if status_code >= 500:
        return True
    return False


def post_with_backoff(
    url: str,
    payload: Dict[str, Any],
    headers: Dict[str, str],
    *,
    max_retries: int = DEFAULT_MAX_RETRIES,
    base_delay: float = DEFAULT_BASE_DELAY,
    max_delay: float = DEFAULT_MAX_DELAY,
    timeout: float = DEFAULT_TIMEOUT,
    session: Optional[requests.Session] = None,
) -> requests.Response:
    """POST JSON with exponential backoff.

    Retries connection/timeouts, HTTP 5xx, and 429. Other 4xx fail immediately.
    """
    poster = session.post if session is not None else requests.post
    last_error: Optional[BaseException] = None

    for attempt in range(max_retries):
        try:
            response = poster(
                url,
                headers=headers,
                json=payload,
                allow_redirects=False,
                timeout=timeout,
            )
        except (requests.exceptions.ConnectionError, requests.exceptions.Timeout) as exc:
            last_error = exc
        except requests.exceptions.RequestException as exc:
            last_error = exc
        else:
            try:
                response.raise_for_status()
                return response
            except requests.exceptions.HTTPError as exc:
                status = getattr(response, "status_code", None)
                wrapped = requests.exceptions.HTTPError(
                    f"{exc}\nResponse body:\n{getattr(response, 'text', '')}",
                    response=response,
                )
                if not _should_retry_status(status):
                    raise wrapped from exc
                last_error = wrapped

        if attempt == max_retries - 1:
            break
        delay = min(base_delay * (2 ** attempt), max_delay)
        time.sleep(delay)

    if last_error is not None:
        raise last_error
    raise RuntimeError(f"Failed to POST {url} after {max_retries} attempts")


def parse_json_response(response: requests.Response) -> Any:
    """Parse a response body that may already be a dict or a JSON string."""
    data = response.json()
    if isinstance(data, str):
        return json.loads(data)
    return data
