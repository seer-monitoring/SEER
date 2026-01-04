package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	apiBase         = "https://api.ansrstudio.com"
	maxRetries      = 5
	baseDelay       = time.Second
	maxDelay        = 30 * time.Second
	maxLogBytes     = 200_000
	failedStorePath = "./failed_payloads"
)

type MonitoringPayload struct {
	JobName      string         `json:"job_name"`
	Status       string         `json:"status"`
	RunID        string         `json:"run_id"`
	StartTime    string         `json:"start_time"`
	EndTime      *string        `json:"end_time"`
	Metadata     map[string]any `json:"metadata"`
	ErrorDetails *string        `json:"error_details"`
	Tags         any            `json:"tags"`
	Logs         *string        `json:"logs"`
}

type StartResponse struct {
	RunID string `json:"run_id"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runCommand()
	case "replay-failed":
		replayFailed()
	default:
		usage()
		os.Exit(1)
	}
}
func replayFailed() {
	apiKey := os.Getenv("SEER_API_KEY")
	if apiKey == "" {
		fmt.Println("SEER_API_KEY not set")
		os.Exit(1)
	}

	files, err := os.ReadDir(failedStorePath)
	if err != nil {
		fmt.Println("No failed payloads found.")
		return
	}

	headers := authHeaders(apiKey)
	replayed := 0

	for _, f := range files {
		if f.IsDir() {
			continue
		}

		path := filepath.Join(failedStorePath, f.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			fmt.Println("Invalid payload:", f.Name())
			continue
		}

		endpoint := endpointFromFilename(f.Name())
		if endpoint == "" {
			continue
		}

		_, err = postWithBackoff(apiBase+endpoint, payload, headers)
		if err != nil {
			fmt.Println("Replay failed:", f.Name())
			continue
		}

		os.Remove(path)
		replayed++
	}

	fmt.Printf("✓ Replayed %d failed payload(s)\n", replayed)
}

func runCommand() {
	jobName := os.Args[2]

	// -------- FLAG PARSING --------
	captureLogs := true
	cmdStart := 3
	var metadata map[string]any
	for i := 3; i < len(os.Args); i++ {
		arg := os.Args[i]

		if strings.HasPrefix(arg, "--capture-logs=") {
			val := strings.TrimPrefix(arg, "--capture-logs=")
			if val == "false" {
				captureLogs = false
			}
			cmdStart = i + 1
			continue
		}

		if strings.HasPrefix(arg, "--metadata=") {
			raw := strings.TrimPrefix(arg, "--metadata=")
			if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
				fmt.Println("Invalid --metadata JSON")
				os.Exit(1)
			}
			cmdStart = i + 1
			continue
		}

		// first non-flag argument is command start
		cmdStart = i
		break
	}
	if len(os.Args) <= cmdStart {
		usage()
		os.Exit(1)
	}

	command := os.Args[cmdStart:]

	apiKey := os.Getenv("SEER_API_KEY")
	if apiKey == "" {
		fmt.Println("SEER_API_KEY not set")
		os.Exit(1)
	}

	startTime := time.Now().UTC().Format(time.RFC3339)
	var runID string
	seerReady := true
	var unsavedPayload = false
	payload := MonitoringPayload{
		JobName:   jobName,
		Status:    "running",
		StartTime: startTime,
		Metadata:  metadata,
	}

	headers := authHeaders(apiKey)

	// -------- START MONITORING --------
	resp, err := postWithBackoff(apiBase+"/monitoring", payload, headers)
	if err != nil {
		fmt.Println(err)
		err = saveFailedPayload(payload, "monitoring")
		if err != nil {
			unsavedPayload = true
		}
		seerReady = false
	} else {
		runID = resp.RunID

		fmt.Printf("✓ Connected to SEER\n✓ Pipeline \"%s\" registered\n", jobName)
	}

	// -------- RUN USER CODE --------
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.Command(command[0], command[1:]...)

	if captureLogs {
		cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
		fmt.Println("✓ Capturing Logs")
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if seerReady {
		fmt.Println("→ Monitoring active.")
	}

	fmt.Println("Starting Code...")

	//startExec := time.Now()
	err = cmd.Run()

	status := "success"
	var errorDetails *string
	if err != nil {
		status = "failed"
		e := err.Error()
		errorDetails = &e
	}

	endTime := time.Now().UTC().Format(time.RFC3339)

	var logs *string
	if captureLogs {
		l := truncate(stdoutBuf.String()+stderrBuf.String(), maxLogBytes)
		logs = &l
	}

	// -------- FINISH MONITORING --------
	if seerReady && runID != "" {
		finalPayload := MonitoringPayload{
			JobName:      jobName,
			Status:       status,
			RunID:        runID,
			StartTime:    startTime,
			EndTime:      &endTime,
			ErrorDetails: errorDetails,
			Logs:         logs,
			Metadata:     metadata,
		}
		_, err := postWithBackoff(apiBase+"/monitoring", finalPayload, headers)
		if err != nil {
			saveFailedPayload(finalPayload, "monitoring")
			fmt.Println("X Failed to send final monitoring payload")
		} else {
			fmt.Println("✓ Monitoring complete.")
		}
	} else {
		if unsavedPayload {
			saveFailedPayload(payload, "monitoring")
		}
		fmt.Println("SEER unable to start.")
	}

	if err != nil {
		os.Exit(1)
	}
}

// ---------------- HELPERS ----------------

func usage() {
	fmt.Println("Usage:")
	fmt.Println("  seer run <job-name> [flags] <command> [args...]")
	fmt.Println("  seer replay-failed")
	fmt.Println("")
	fmt.Println("Flags:")
	fmt.Println("  --capture-logs=false")
	fmt.Println("  --metadata=<json>")
}

func endpointFromFilename(name string) string {
	if strings.HasPrefix(name, "monitoring_") {
		return "/monitoring"
	}
	if strings.HasPrefix(name, "heartbeat_") {
		return "/heartbeat"
	}
	return ""
}

func postWithBackoff(url string, payload any, headers map[string]string) (StartResponse, error) {
	body, _ := json.Marshal(payload)
	var ResponseBody StartResponse
	var raw string
	for attempt := 0; attempt < maxRetries; attempt++ {
		req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		client := &http.Client{Timeout: 100 * time.Second}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode < 300 {
			body, err := io.ReadAll(resp.Body)

			if err != nil {
				fmt.Println("Error reading response:", err)
				return ResponseBody, errors.New("Unable To Read Response.")
			}

			if err := json.Unmarshal(body, &raw); err != nil {
				fmt.Println("Error unmarshaling JSON:", err)
				return ResponseBody, errors.New("Error unmarshaling JSON.")
			}

			if err := json.Unmarshal([]byte(raw), &ResponseBody); err != nil {
				fmt.Println("Error unmarshaling JSON:", err)
				return ResponseBody, errors.New("Error unmarshaling JSON.")
			}
			return ResponseBody, nil
		}

		if attempt == maxRetries-1 {
			return ResponseBody, errors.New("X Error Connecting to SEER. Continuing without SEER Monitoring. Please Check https://status.seer.ansrstudio.com")
		}

		delay := time.Duration(1<<attempt) * baseDelay
		if delay > maxDelay {
			delay = maxDelay
		}
		time.Sleep(delay)
	}
	return ResponseBody, errors.New("unreachable")
}

func authHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization": apiKey,
		"Content-Type":  "application/json",
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func saveFailedPayload(payload any, kind string) error {
	if err := os.MkdirAll(failedStorePath, 0755); err != nil {
		return err
	}

	ts := time.Now().UnixNano()
	path := filepath.Join(
		failedStorePath,
		fmt.Sprintf("%s_%d.json", kind, ts),
	)

	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, b, 0644); err != nil {
		return err
	}

	return nil
}
