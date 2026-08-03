package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
			}
		}

		if attempt == maxRetries-1 {
			break
		}
		delay := baseDelay << attempt
		if delay > maxDelay {
			delay = maxDelay
		}
		time.Sleep(delay)
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
