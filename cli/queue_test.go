package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestParseJSONBodyDictAndString(t *testing.T) {
	m, err := parseJSONBody([]byte(`{"run_id":"abc"}`))
	if err != nil || m["run_id"] != "abc" {
		t.Fatalf("dict body: %v %#v", err, m)
	}

	m, err = parseJSONBody([]byte(`"{\"run_id\":\"xyz\"}"`))
	if err != nil || m["run_id"] != "xyz" {
		t.Fatalf("string body: %v %#v", err, m)
	}
}

func TestPostWithBackoffNoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	client := newHTTPClient(5)
	_, err := postWithBackoff(client, srv.URL, map[string]any{}, authHeaders("k", "id"), 5)
	if err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestPostWithBackoffRetries5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := newHTTPClient(5)
	_, err := postWithBackoff(client, srv.URL, map[string]any{}, authHeaders("k", "id"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestSaveAndReplayMonitoringOffline(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SEER_QUEUE_DIR", dir)

	var registerHits, completeHits int32
	var sawRegisterKey, sawCompleteKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		status, _ := body["status"].(string)
		if status == "running" {
			atomic.AddInt32(&registerHits, 1)
			sawRegisterKey = key
			_, _ = w.Write([]byte(`{"run_id":"assigned-1"}`))
			return
		}
		atomic.AddInt32(&completeHits, 1)
		sawCompleteKey = key
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	path, err := saveFailedPayload(map[string]any{
		"job_name":   "j",
		"status":     "success",
		"run_id":     "",
		"start_time": "2026-01-01T00:00:00Z",
		"end_time":   "2026-01-01T00:01:00Z",
		"logs":       "offline logs",
	}, "monitoring", "offline-key", srv.URL, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	result := replayFailedPayloads("key", "https://ignored.example", dir, 5)
	if result.Sent != 1 || result.Failed != 0 {
		t.Fatalf("replay result: %+v", result)
	}
	if atomic.LoadInt32(&registerHits) != 1 || atomic.LoadInt32(&completeHits) != 1 {
		t.Fatalf("hits register=%d complete=%d", registerHits, completeHits)
	}
	if sawRegisterKey != "offline-key:register" || sawCompleteKey != "offline-key:complete" {
		t.Fatalf("keys register=%q complete=%q", sawRegisterKey, sawCompleteKey)
	}
	remaining, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(remaining) != 0 {
		t.Fatalf("expected empty queue, got %v", remaining)
	}
}

func TestFIFOEvictionByMaxFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SEER_QUEUE_DIR", dir)
	t.Setenv("SEER_QUEUE_MAX_FILES", "2")
	t.Setenv("SEER_QUEUE_MAX_BYTES", "10485760")

	first, err := saveFailedPayload(map[string]any{"n": 1}, "monitoring", "a", "https://example.com", dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := saveFailedPayload(map[string]any{"n": 2}, "heartbeat", "b", "https://example.com", dir)
	if err != nil {
		t.Fatal(err)
	}
	third, err := saveFailedPayload(map[string]any{"n": 3}, "monitoring", "c", "https://example.com", dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("expected first envelope evicted")
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(third); err != nil {
		t.Fatal(err)
	}
}

func TestDeadLetterAfterMaxAttempts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SEER_QUEUE_DIR", dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	path, err := saveFailedPayload(map[string]any{"job_name": "j"}, "heartbeat", "k", srv.URL, dir)
	if err != nil {
		t.Fatal(err)
	}
	env, err := loadEnvelope(path)
	if err != nil {
		t.Fatal(err)
	}
	env.Attempts = 4
	if err := atomicWriteJSON(path, env); err != nil {
		t.Fatal(err)
	}

	result := replayFailedPayloads("key", srv.URL, dir, 5)
	if result.DeadLettered != 1 {
		t.Fatalf("expected dead letter, got %+v", result)
	}
	dead, _ := filepath.Glob(filepath.Join(dir, "dead", "*.json"))
	if len(dead) != 1 {
		t.Fatalf("expected 1 dead file, got %v", dead)
	}
}

func TestLegacyRawPayloadStillReplays(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SEER_QUEUE_DIR", dir)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			_, _ = w.Write([]byte(`{"run_id":"legacy-run"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	legacy := filepath.Join(dir, "monitoring_20200101000000.json")
	if err := os.WriteFile(legacy, []byte(`{"job_name":"legacy","status":"success","run_id":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Rewrite legacy payload base by saving envelope then replacing with legacy format
	// already wrote legacy file; pin base_url via a wrapper isn't possible — inject by
	// writing an envelope-compatible legacy that loadEnvelope upgrades, then override base.
	env, err := loadEnvelope(legacy)
	if err != nil {
		t.Fatal(err)
	}
	env.BaseURL = srv.URL
	if err := atomicWriteJSON(legacy, env); err != nil {
		t.Fatal(err)
	}

	result := replayFailedPayloads("key", srv.URL, dir, 5)
	if result.Sent != 1 {
		t.Fatalf("expected sent=1, got %+v", result)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("expected register+complete, hits=%d", hits)
	}
}

func TestResolveBaseURLPrecedence(t *testing.T) {
	t.Setenv("SEER_BASE_URL", "https://seer.env.example/")
	if got := resolveBaseURL(""); got != "https://seer.env.example" {
		t.Fatalf("env: %s", got)
	}
	if got := resolveBaseURL("https://seer.explicit.example/"); got != "https://seer.explicit.example" {
		t.Fatalf("explicit: %s", got)
	}
}

func TestParseTags(t *testing.T) {
	if got := parseTags("etl, prod"); len(got) != 2 || got[0] != "etl" || got[1] != "prod" {
		t.Fatalf("csv: %#v", got)
	}
	if got := parseTags(`["a","b"]`); len(got) != 2 || got[0] != "a" {
		t.Fatalf("json: %#v", got)
	}
}
