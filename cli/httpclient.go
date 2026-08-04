package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

type httpError struct {
	StatusCode int
	Body       string
	Err        error
}

func (e *httpError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%v\nResponse body:\n%s", e.Err, e.Body)
	}
	return e.Err.Error()
}

func shouldRetryStatus(statusCode int) bool {
	if statusCode == 429 {
		return true
	}
	return statusCode >= 500
}

func newHTTPClient(timeoutSec int) *http.Client {
	return &http.Client{
		Timeout: time.Duration(timeoutSec) * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func authHeaders(apiKey, idempotencyKey string) map[string]string {
	headers := map[string]string{
		"Authorization": apiKey,
		"Content-Type":  "application/json",
	}
	if idempotencyKey != "" {
		headers["Idempotency-Key"] = idempotencyKey
	}
	return headers
}

// computeBackoffDelay returns a full-jitter delay for attempt (0-based).
// On 429, prefers Retry-After when it is a numeric second count.
func computeBackoffDelay(attempt int, baseDelay, maxDelay time.Duration, resp *http.Response) time.Duration {
	if resp != nil && resp.StatusCode == 429 {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.ParseFloat(ra, 64); err == nil && secs >= 0 {
				d := time.Duration(secs * float64(time.Second))
				if d > maxDelay {
					return maxDelay
				}
				return d
			}
		}
	}
	ceiling := float64(baseDelay) * math.Pow(2, float64(attempt))
	if ceiling > float64(maxDelay) {
		ceiling = float64(maxDelay)
	}
	return time.Duration(rand.Float64() * ceiling)
}

func postWithBackoff(
	client *http.Client,
	url string,
	payload any,
	headers map[string]string,
	maxRetries int,
) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	var lastErr error
	baseDelay := time.Second
	maxDelay := 30 * time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		var respForDelay *http.Response
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				parsed, parseErr := parseJSONBody(respBody)
				if parseErr != nil {
					return nil, parseErr
				}
				return parsed, nil
			} else {
				httpErr := &httpError{
					StatusCode: resp.StatusCode,
					Body:       string(respBody),
					Err:        fmt.Errorf("HTTP %d", resp.StatusCode),
				}
				if !shouldRetryStatus(resp.StatusCode) {
					return nil, httpErr
				}
				lastErr = httpErr
				// Rebuild a lightweight header holder for Retry-After.
				respForDelay = &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
				}
			}
		}

		if attempt == maxRetries-1 {
			break
		}
		time.Sleep(computeBackoffDelay(attempt, baseDelay, maxDelay, respForDelay))
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("failed to POST %s after %d attempts", url, maxRetries)
}

func parseJSONBody(raw []byte) (map[string]any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err == nil {
		return asMap, nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err != nil {
		return nil, fmt.Errorf("error unmarshaling JSON: %w", err)
	}
	if err := json.Unmarshal([]byte(asString), &asMap); err != nil {
		return nil, fmt.Errorf("error unmarshaling nested JSON: %w", err)
	}
	return asMap, nil
}
