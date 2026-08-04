package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
)

var endpointPaths = map[string]string{
	"monitoring": "/monitoring",
	"heartbeat":  "/heartbeat",
}

type Envelope struct {
	Version        int            `json:"version"`
	Endpoint       string         `json:"endpoint"`
	BaseURL        string         `json:"base_url"`
	Payload        map[string]any `json:"payload"`
	CreatedAt      string         `json:"created_at"`
	Attempts       int            `json:"attempts"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type ReplayResult struct {
	Sent         int
	Failed       int
	DeadLettered int
	Skipped      bool
	Errors       []string
}

func ensureQueueDir(queueDir string) (string, error) {
	if queueDir == "" {
		queueDir = getQueueDir()
	}
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(queueDir, "dead"), 0o755); err != nil {
		return "", err
	}
	return queueDir, nil
}

func utcNowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func atomicWriteJSON(filepathName string, data any) error {
	dir := filepath.Dir(filepathName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%s.tmp", filepathName, uuid.NewString())
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepathName); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func listQueueFiles(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".sending") {
			names = append(names, name)
		}
	}
	// Lexicographic sort is FIFO: filenames start with UTC timestamps.
	sort.Strings(names)
	return names, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func enforceQueueLimits(queueDir string) (int, error) {
	path, err := ensureQueueDir(queueDir)
	if err != nil {
		return 0, err
	}
	maxFiles, maxBytes := getQueueLimits()

	lock := flock.New(filepath.Join(path, ".queue.lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return 0, err
	}
	defer lock.Unlock()

	evicted := 0
	for {
		files, err := listQueueFiles(path)
		if err != nil {
			return evicted, err
		}
		var total int64
		for _, name := range files {
			total += fileSize(filepath.Join(path, name))
		}
		if len(files) <= maxFiles && total <= int64(maxBytes) {
			break
		}
		if len(files) <= 1 {
			break
		}
		oldest := files[0]
		oldestPath := filepath.Join(path, oldest)
		if err := os.Remove(oldestPath); err != nil {
			break
		}
		evicted++
		fmt.Printf("Seer queue limit reached; evicted oldest envelope: %s\n", oldest)
	}
	return evicted, nil
}

func saveFailedPayload(
	payload map[string]any,
	endpoint string,
	idempotencyKey string,
	baseURL string,
	queueDir string,
) (string, error) {
	if _, ok := endpointPaths[endpoint]; !ok {
		return "", fmt.Errorf("unknown endpoint: %s", endpoint)
	}
	path, err := ensureQueueDir(queueDir)
	if err != nil {
		return "", err
	}
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}
	now := time.Now().UTC()
	stamp := now.Format("20060102150405") + fmt.Sprintf("%06d", now.Nanosecond()/1000)
	filename := fmt.Sprintf("%s_%s_%s.json", stamp, endpoint, uuid.NewString()[:8])
	filepathName := filepath.Join(path, filename)
	envelope := Envelope{
		Version:        envelopeVersion,
		Endpoint:       endpoint,
		BaseURL:        resolveBaseURL(baseURL),
		Payload:        payload,
		CreatedAt:      utcNowISO(),
		Attempts:       0,
		IdempotencyKey: idempotencyKey,
	}
	if err := atomicWriteJSON(filepathName, envelope); err != nil {
		return "", err
	}
	_, _ = enforceQueueLimits(path)
	fmt.Printf("Seer upload failed, queued at %s\n", filepathName)
	fmt.Println("Call `seer replay` to retrigger events.")
	return filepathName, nil
}

func loadEnvelope(filepathName string) (Envelope, error) {
	b, err := os.ReadFile(filepathName)
	if err != nil {
		return Envelope{}, err
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		return Envelope{}, err
	}

	_, hasPayload := data["payload"]
	_, hasEndpoint := data["endpoint"]
	if !hasPayload || !hasEndpoint {
		basename := filepath.Base(filepathName)
		endpoint := ""
		switch {
		case strings.Contains(basename, "monitoring"):
			endpoint = "monitoring"
		case strings.Contains(basename, "heartbeat"):
			endpoint = "heartbeat"
		default:
			return Envelope{}, fmt.Errorf("cannot infer endpoint for legacy file: %s", basename)
		}
		return Envelope{
			Version:        0,
			Endpoint:       endpoint,
			BaseURL:        resolveBaseURL(""),
			Payload:        data,
			CreatedAt:      utcNowISO(),
			Attempts:       0,
			IdempotencyKey: uuid.NewString(),
		}, nil
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, err
	}
	if env.IdempotencyKey == "" {
		env.IdempotencyKey = uuid.NewString()
	}
	if env.BaseURL == "" {
		env.BaseURL = resolveBaseURL("")
	}
	if env.Payload == nil {
		env.Payload = map[string]any{}
	}
	return env, nil
}

func endpointURL(baseURL, endpoint string) (string, error) {
	path, ok := endpointPaths[endpoint]
	if !ok {
		return "", fmt.Errorf("unknown endpoint: %s", endpoint)
	}
	return strings.TrimRight(baseURL, "/") + path, nil
}

func deliverMonitoringPayload(
	client *http.Client,
	url string,
	payload map[string]any,
	apiKey, idempotencyKey string,
) (map[string]any, error) {
	body := copyMap(payload)
	runID, _ := body["run_id"].(string)

	if runID == "" {
		registerPayload := map[string]any{
			"job_name":      body["job_name"],
			"status":        "running",
			"run_id":        "",
			"start_time":    body["start_time"],
			"end_time":      nil,
			"metadata":      body["metadata"],
			"error_details": nil,
			"tags":          body["tags"],
			"logs":          nil,
		}
		headers := authHeaders(apiKey, idempotencyKey+":register")
		registered, err := postWithBackoff(client, url, registerPayload, headers, defaultMaxAttempts)
		if err != nil {
			return nil, err
		}
		runID, _ = registered["run_id"].(string)
		if runID == "" {
			return nil, fmt.Errorf("seer register succeeded but returned no run_id during offline replay")
		}
		body["run_id"] = runID
	}

	headers := authHeaders(apiKey, idempotencyKey+":complete")
	if _, err := postWithBackoff(client, url, body, headers, defaultMaxAttempts); err != nil {
		return nil, err
	}
	return body, nil
}

func deliverEnvelope(
	client *http.Client,
	endpoint, url string,
	payload map[string]any,
	apiKey, idempotencyKey string,
) (map[string]any, error) {
	if endpoint == "monitoring" {
		return deliverMonitoringPayload(client, url, payload, apiKey, idempotencyKey)
	}
	headers := authHeaders(apiKey, idempotencyKey)
	_, err := postWithBackoff(client, url, payload, headers, defaultMaxAttempts)
	return nil, err
}

func replayFailedPayloads(
	apiKey string,
	baseURL string,
	queueDir string,
	maxAttempts int,
) ReplayResult {
	result := ReplayResult{}
	path, err := ensureQueueDir(queueDir)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		result.Failed++
		return result
	}
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	fallbackBase := resolveBaseURL(baseURL)
	client := newHTTPClient(getTimeout())

	lock := flock.New(filepath.Join(path, ".replay.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		result.Failed++
		return result
	}
	if !locked {
		result.Skipped = true
		fmt.Println("Seer queue replay already in progress; skipping.")
		return result
	}
	defer lock.Unlock()

	files, err := listQueueFiles(path)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		result.Failed++
		return result
	}

	for _, filename := range files {
		filepathName := filepath.Join(path, filename)
		claimed := filepathName + ".sending"
		if err := os.Rename(filepathName, claimed); err != nil {
			continue
		}

		envelope, loadErr := loadEnvelope(claimed)
		if loadErr == nil {
			targetBase := envelope.BaseURL
			if targetBase == "" {
				targetBase = fallbackBase
			}
			envelope.BaseURL = targetBase
			url, urlErr := endpointURL(targetBase, envelope.Endpoint)
			idemKey := envelope.IdempotencyKey
			if idemKey == "" {
				idemKey = uuid.NewString()
				envelope.IdempotencyKey = idemKey
			}
			if urlErr == nil {
				delivered, deliverErr := deliverEnvelope(
					client,
					envelope.Endpoint,
					url,
					envelope.Payload,
					apiKey,
					idemKey,
				)
				if deliverErr == nil {
					if delivered != nil {
						envelope.Payload = delivered
					}
					os.Remove(claimed)
					result.Sent++
					fmt.Printf("Successfully replayed %s event to SEER\n", envelope.Endpoint)
					continue
				}
				loadErr = deliverErr
			} else {
				loadErr = urlErr
			}
		}

		retryEnv := safeLoadForRetry(claimed, fallbackBase)
		retryEnv.Attempts++
		if retryEnv.IdempotencyKey == "" {
			retryEnv.IdempotencyKey = uuid.NewString()
		}
		if retryEnv.BaseURL == "" {
			retryEnv.BaseURL = fallbackBase
		}
		if retryEnv.Attempts >= maxAttempts {
			deadPath := filepath.Join(path, "dead", filepath.Base(filepathName))
			_ = atomicWriteJSON(deadPath, retryEnv)
			os.Remove(claimed)
			result.DeadLettered++
			msg := fmt.Sprintf("Moved to dead letter after %d attempts: %s", maxAttempts, filename)
			result.Errors = append(result.Errors, msg)
			fmt.Println(msg)
		} else {
			_ = atomicWriteJSON(filepathName, retryEnv)
			os.Remove(claimed)
			result.Failed++
			msg := fmt.Sprintf("Unable to send payload (%s): %v", filename, loadErr)
			result.Errors = append(result.Errors, msg)
			fmt.Println(msg)
		}
	}
	return result
}

func safeLoadForRetry(claimedPath, fallbackBase string) Envelope {
	env, err := loadEnvelope(claimedPath)
	if err == nil {
		return env
	}
	return Envelope{
		Version:        envelopeVersion,
		Endpoint:       "monitoring",
		BaseURL:        fallbackBase,
		Payload:        map[string]any{},
		CreatedAt:      utcNowISO(),
		Attempts:       0,
		IdempotencyKey: uuid.NewString(),
	}
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type QueueStatus struct {
	Pending       int
	Sending       int
	Dead          int
	PendingBytes  int64
	DeadBytes     int64
	OldestPending string
	MaxFiles      int
	MaxBytes      int
	QueueDir      string
}

type DeadLetterSummary struct {
	File      string
	Path      string
	Endpoint  string
	JobName   string
	Status    string
	Attempts  int
	CreatedAt string
	Error     string
}

func queueStatus(queueDir string) (QueueStatus, error) {
	path, err := ensureQueueDir(queueDir)
	if err != nil {
		return QueueStatus{}, err
	}
	maxFiles, maxBytes := getQueueLimits()
	st := QueueStatus{MaxFiles: maxFiles, MaxBytes: maxBytes, QueueDir: path}

	files, err := listQueueFiles(path)
	if err != nil {
		return st, err
	}
	st.Pending = len(files)
	if len(files) > 0 {
		st.OldestPending = files[0]
	}
	for _, name := range files {
		st.PendingBytes += fileSize(filepath.Join(path, name))
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return st, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json.sending") {
			st.Sending++
		}
	}

	deadDir := filepath.Join(path, "dead")
	deadEntries, err := os.ReadDir(deadDir)
	if err != nil {
		return st, err
	}
	for _, e := range deadEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		st.Dead++
		st.DeadBytes += fileSize(filepath.Join(deadDir, e.Name()))
	}
	return st, nil
}

func listDeadLetters(queueDir string) ([]DeadLetterSummary, error) {
	path, err := ensureQueueDir(queueDir)
	if err != nil {
		return nil, err
	}
	deadDir := filepath.Join(path, "dead")
	entries, err := os.ReadDir(deadDir)
	if err != nil {
		return nil, err
	}
	var out []DeadLetterSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		full := filepath.Join(deadDir, e.Name())
		env, loadErr := loadEnvelope(full)
		sum := DeadLetterSummary{File: e.Name(), Path: full}
		if loadErr != nil {
			sum.Error = loadErr.Error()
			out = append(out, sum)
			continue
		}
		sum.Endpoint = env.Endpoint
		sum.Attempts = env.Attempts
		sum.CreatedAt = env.CreatedAt
		if env.Payload != nil {
			if v, ok := env.Payload["job_name"].(string); ok {
				sum.JobName = v
			}
			if v, ok := env.Payload["status"].(string); ok {
				sum.Status = v
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

func retryDeadLetters(queueDir string, all bool, files []string) (int, []string, error) {
	path, err := ensureQueueDir(queueDir)
	if err != nil {
		return 0, nil, err
	}
	deadDir := filepath.Join(path, "dead")
	var targets []string
	if all {
		entries, err := os.ReadDir(deadDir)
		if err != nil {
			return 0, nil, err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				targets = append(targets, filepath.Join(deadDir, e.Name()))
			}
		}
	} else {
		for _, f := range files {
			candidate := f
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(deadDir, filepath.Base(candidate))
			}
			targets = append(targets, candidate)
		}
	}

	restored := 0
	var errs []string
	for _, deadPath := range targets {
		env, loadErr := loadEnvelope(deadPath)
		if loadErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", deadPath, loadErr))
			continue
		}
		env.Attempts = 0
		if env.IdempotencyKey == "" {
			env.IdempotencyKey = uuid.NewString()
		}
		dest := filepath.Join(path, filepath.Base(deadPath))
		if err := atomicWriteJSON(dest, env); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", deadPath, err))
			continue
		}
		if err := os.Remove(deadPath); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", deadPath, err))
			continue
		}
		restored++
	}
	return restored, errs, nil
}
