"""Shared HTTP helpers for the Seer client."""

from __future__ import annotations

import json
import os
import random
import time
from typing import Any, Dict, Optional

import requests

DEFAULT_TIMEOUT = 30
DEFAULT_MAX_RETRIES = 5
DEFAULT_BASE_DELAY = 1
DEFAULT_MAX_DELAY = 30
DEFAULT_REPLAY_JITTER_MS = 2000


def _should_retry_status(status_code: Optional[int]) -> bool:
    """Retry on server errors and rate limits; never on other 4xx."""
    if status_code is None:
        return True
    if status_code == 429:
        return True
    if status_code >= 500:
        return True
    return False


def compute_backoff_delay(
    attempt: int,
    *,
    base_delay: float = DEFAULT_BASE_DELAY,
    max_delay: float = DEFAULT_MAX_DELAY,
    response: Optional[requests.Response] = None,
    rng: Optional[random.Random] = None,
) -> float:
    """Full-jitter exponential backoff delay for the given attempt index.

    Uses ``random.uniform(0, min(base * 2**attempt, max))``. On HTTP 429, prefers
    a numeric ``Retry-After`` header when present (capped at ``max_delay``).
    """
    if response is not None and getattr(response, "status_code", None) == 429:
        ra = response.headers.get("Retry-After") if response.headers else None
        if ra is not None:
            try:
                return min(float(ra), max_delay)
            except (TypeError, ValueError):
                pass
    ceiling = min(base_delay * (2**attempt), max_delay)
    picker = rng.uniform if rng is not None else random.uniform
    return picker(0.0, ceiling)


def replay_startup_jitter_seconds(
    *,
    rng: Optional[random.Random] = None,
) -> float:
    """Sleep budget before auto-replay / first background flush (stampede guard)."""
    raw = os.getenv("SEER_REPLAY_JITTER_MS", str(DEFAULT_REPLAY_JITTER_MS)).strip()
    try:
        ms = int(raw)
    except ValueError:
        ms = DEFAULT_REPLAY_JITTER_MS
    if ms <= 0:
        return 0.0
    picker = rng.uniform if rng is not None else random.uniform
    return picker(0.0, ms / 1000.0)


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
    rng: Optional[random.Random] = None,
) -> requests.Response:
    """POST JSON with full-jitter exponential backoff.

    Retries connection/timeouts, HTTP 5xx, and 429. Other 4xx fail immediately.
    """
    poster = session.post if session is not None else requests.post
    last_error: Optional[BaseException] = None
    last_response: Optional[requests.Response] = None

    for attempt in range(max_retries):
        last_response = None
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
            last_response = response
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
        delay = compute_backoff_delay(
            attempt,
            base_delay=base_delay,
            max_delay=max_delay,
            response=last_response,
            rng=rng,
        )
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
