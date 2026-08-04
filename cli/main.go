package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		os.Exit(runCommand(os.Args[2:]))
	case "heartbeat":
		os.Exit(heartbeatCommand(os.Args[2:]))
	case "replay", "replay-failed":
		os.Exit(replayCommand(os.Args[2:]))
	case "queue":
		os.Exit(queueCommand(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
	case "version", "--version":
		fmt.Println("seer-cli 0.2.4")
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`Usage:
  seer run <job-name> [flags] [--] <command> [args...]
  seer heartbeat <job-name> [flags]
  seer replay [--max-attempts=N]
  seer queue status|flush|list-dead|retry-dead [flags]
  seer version

Flags for run:
  --capture-logs=true|false   Capture stdout/stderr (default true)
  --metadata=<json>           JSON object attached to the run
  --tags=a,b,c                Comma-separated tags
  --base-url=<url>            Override SEER_BASE_URL
  --no-auto-replay            Skip one-shot queue flush on start
  --background-replay         Flush queue periodically while the job runs
  --replay-interval=<sec>     Background flush interval (default 60)

Flags for heartbeat:
  --metadata=<json>
  --tags=a,b,c
  --base-url=<url>
  --no-auto-replay

Queue commands:
  seer queue status
  seer queue flush [--max-attempts=N] [--base-url=...]
  seer queue list-dead
  seer queue retry-dead --all | <file...> [--no-flush] [--max-attempts=N]

Environment:
  SEER_API_KEY            Required API key
  SEER_BASE_URL           API host (default https://api.ansrstudio.com)
  SEER_QUEUE_DIR          Offline queue dir (default ~/.seer/queue)
  SEER_QUEUE_MAX_FILES    Max queued envelopes (default 500)
  SEER_QUEUE_MAX_BYTES    Max queue size in bytes (default 50 MiB)
  SEER_TIMEOUT            HTTP timeout seconds (default 30)
  SEER_REPLAY_JITTER_MS   Max startup jitter before auto-replay (default 2000)`)
}

type runOptions struct {
	jobName           string
	command           []string
	captureLogs       bool
	metadata          map[string]any
	tags              []string
	baseURL           string
	autoReplay        bool
	backgroundReplay  bool
	replayIntervalSec int
}

type heartbeatOptions struct {
	jobName    string
	metadata   map[string]any
	tags       []string
	baseURL    string
	autoReplay bool
}

func runCommand(args []string) int {
	opts, err := parseRunArgs(args)
	if err != nil {
		fmt.Println(err)
		usage()
		return 1
	}

	apiKey := resolveAPIKey()
	if apiKey == "" {
		fmt.Println("SEER_API_KEY not set")
		return 1
	}
	baseURL := resolveBaseURL(opts.baseURL)
	client := newHTTPClient(getTimeout())

	if opts.autoReplay {
		if j := getReplayJitter(); j > 0 {
			time.Sleep(j)
		}
		result := replayFailedPayloads(apiKey, baseURL, getQueueDir(), defaultMaxAttempts)
		if result.Sent+result.Failed+result.DeadLettered > 0 || result.Skipped {
			fmt.Printf("Auto-replay: sent=%d failed=%d dead_lettered=%d skipped=%v\n",
				result.Sent, result.Failed, result.DeadLettered, result.Skipped)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if opts.backgroundReplay {
		go backgroundReplayLoop(ctx, apiKey, baseURL, opts.replayIntervalSec)
	}

	startTime := time.Now().UTC().Format(time.RFC3339)
	startPayload := map[string]any{
		"job_name":      opts.jobName,
		"status":        "running",
		"run_id":        "",
		"start_time":    startTime,
		"end_time":      nil,
		"metadata":      opts.metadata,
		"error_details": nil,
		"tags":          opts.tags,
		"logs":          nil,
	}

	var runID string
	startKey := uuid.NewString()
	startResp, err := postWithBackoff(
		client,
		baseURL+"/monitoring",
		startPayload,
		authHeaders(apiKey, startKey),
		defaultMaxAttempts,
	)
	if err != nil {
		fmt.Println(err)
		fmt.Println("Seer unavailable at start; will queue final result if needed.")
	} else {
		if id, ok := startResp["run_id"].(string); ok {
			runID = id
		}
		fmt.Println("✓ Connected to SEER")
		fmt.Printf("✓ Pipeline %q registered\n", opts.jobName)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.Command(opts.command[0], opts.command[1:]...)
	if opts.captureLogs {
		cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
		if runID != "" {
			fmt.Println("✓ Capturing Logs")
		}
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if runID != "" {
		fmt.Println("→ Monitoring active.")
	}
	fmt.Println("Starting Code...")

	runErr := cmd.Run()
	status := "success"
	var errorDetails any
	exitCode := 0
	if runErr != nil {
		status = "failed"
		errorDetails = runErr.Error()
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	endTime := time.Now().UTC().Format(time.RFC3339)
	var logs any
	if opts.captureLogs {
		logs = truncate(stdoutBuf.String()+stderrBuf.String(), maxLogBytes)
	}

	finalPayload := map[string]any{
		"job_name":      opts.jobName,
		"status":        status,
		"run_id":        runID,
		"start_time":    startTime,
		"end_time":      endTime,
		"metadata":      opts.metadata,
		"error_details": errorDetails,
		"tags":          opts.tags,
		"logs":          logs,
	}

	completeKey := uuid.NewString()
	if runID != "" {
		_, err := postWithBackoff(
			client,
			baseURL+"/monitoring",
			finalPayload,
			authHeaders(apiKey, completeKey),
			defaultMaxAttempts,
		)
		if err != nil {
			_, _ = saveFailedPayload(finalPayload, "monitoring", completeKey, baseURL, getQueueDir())
			fmt.Printf("Seer completion upload failed; queued for replay: %v\n", err)
		} else {
			fmt.Println("✓ Monitoring complete.")
		}
	} else {
		_, _ = saveFailedPayload(finalPayload, "monitoring", completeKey, baseURL, getQueueDir())
		fmt.Println("Seer unable to start; final result queued for replay.")
	}

	cancel()
	return exitCode
}

func heartbeatCommand(args []string) int {
	opts, err := parseHeartbeatArgs(args)
	if err != nil {
		fmt.Println(err)
		usage()
		return 1
	}
	apiKey := resolveAPIKey()
	if apiKey == "" {
		fmt.Println("SEER_API_KEY not set")
		return 1
	}
	baseURL := resolveBaseURL(opts.baseURL)
	client := newHTTPClient(getTimeout())

	if opts.autoReplay {
		if j := getReplayJitter(); j > 0 {
			time.Sleep(j)
		}
		_ = replayFailedPayloads(apiKey, baseURL, getQueueDir(), defaultMaxAttempts)
	}

	payload := map[string]any{
		"job_name":     opts.jobName,
		"current_time": time.Now().UTC().Format(time.RFC3339),
		"metadata":     opts.metadata,
		"tags":         opts.tags,
	}
	idemKey := uuid.NewString()
	_, err = postWithBackoff(
		client,
		baseURL+"/heartbeat",
		payload,
		authHeaders(apiKey, idemKey),
		defaultMaxAttempts,
	)
	if err != nil {
		_, _ = saveFailedPayload(payload, "heartbeat", idemKey, baseURL, getQueueDir())
		fmt.Printf("Heartbeat queued for replay: %v\n", err)
		return 0
	}
	fmt.Println("Heartbeat received")
	return 0
}

func replayCommand(args []string) int {
	apiKey := resolveAPIKey()
	if apiKey == "" {
		fmt.Println("SEER_API_KEY not set")
		return 1
	}
	baseURL := resolveBaseURL("")
	maxAttempts := defaultMaxAttempts
	for _, arg := range args {
		if strings.HasPrefix(arg, "--max-attempts=") {
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--max-attempts="))
			if err != nil || n <= 0 {
				fmt.Println("Invalid --max-attempts")
				return 1
			}
			maxAttempts = n
		} else if strings.HasPrefix(arg, "--base-url=") {
			baseURL = resolveBaseURL(strings.TrimPrefix(arg, "--base-url="))
		} else {
			fmt.Println("Unknown flag:", arg)
			return 1
		}
	}

	result := replayFailedPayloads(apiKey, baseURL, getQueueDir(), maxAttempts)
	fmt.Printf("✓ Replay complete: sent=%d failed=%d dead_lettered=%d skipped=%v\n",
		result.Sent, result.Failed, result.DeadLettered, result.Skipped)
	if result.Failed > 0 || result.DeadLettered > 0 {
		return 1
	}
	return 0
}

func queueCommand(args []string) int {
	if len(args) < 1 {
		fmt.Println("queue subcommand required: status | flush | list-dead | retry-dead")
		return 1
	}
	switch args[0] {
	case "status":
		st, err := queueStatus(getQueueDir())
		if err != nil {
			fmt.Println(err)
			return 1
		}
		fmt.Printf("queue_dir=%s\n", st.QueueDir)
		fmt.Printf("pending=%d (bytes=%d) sending=%d dead=%d (bytes=%d)\n",
			st.Pending, st.PendingBytes, st.Sending, st.Dead, st.DeadBytes)
		fmt.Printf("limits: max_files=%d max_bytes=%d\n", st.MaxFiles, st.MaxBytes)
		if st.OldestPending != "" {
			fmt.Printf("oldest_pending=%s\n", st.OldestPending)
		}
		return 0
	case "flush":
		return replayCommand(args[1:])
	case "list-dead":
		items, err := listDeadLetters(getQueueDir())
		if err != nil {
			fmt.Println(err)
			return 1
		}
		if len(items) == 0 {
			fmt.Println("No dead-letter envelopes.")
			return 0
		}
		for _, it := range items {
			if it.Error != "" {
				fmt.Printf("%s  error=%s\n", it.File, it.Error)
				continue
			}
			fmt.Printf("%s  endpoint=%s job=%s status=%s attempts=%d\n",
				it.File, it.Endpoint, it.JobName, it.Status, it.Attempts)
		}
		return 0
	case "retry-dead":
		apiKey := resolveAPIKey()
		baseURL := resolveBaseURL("")
		maxAttempts := defaultMaxAttempts
		all := false
		flush := true
		var files []string
		for _, arg := range args[1:] {
			switch {
			case arg == "--all":
				all = true
			case arg == "--no-flush":
				flush = false
			case strings.HasPrefix(arg, "--max-attempts="):
				n, err := strconv.Atoi(strings.TrimPrefix(arg, "--max-attempts="))
				if err != nil || n <= 0 {
					fmt.Println("Invalid --max-attempts")
					return 1
				}
				maxAttempts = n
			case strings.HasPrefix(arg, "--base-url="):
				baseURL = resolveBaseURL(strings.TrimPrefix(arg, "--base-url="))
			case strings.HasPrefix(arg, "--"):
				fmt.Println("Unknown flag:", arg)
				return 1
			default:
				files = append(files, arg)
			}
		}
		if !all && len(files) == 0 {
			fmt.Println("retry-dead requires --all or one or more dead letter filenames")
			return 1
		}
		restored, errs, err := retryDeadLetters(getQueueDir(), all, files)
		if err != nil {
			fmt.Println(err)
			return 1
		}
		for _, e := range errs {
			fmt.Println(e)
		}
		fmt.Printf("Restored %d dead-letter envelope(s) to pending.\n", restored)
		if !flush || restored == 0 {
			if len(errs) > 0 {
				return 1
			}
			return 0
		}
		if apiKey == "" {
			fmt.Println("SEER_API_KEY not set; restored to pending but did not flush. Run `seer queue flush`.")
			return 1
		}
		result := replayFailedPayloads(apiKey, baseURL, getQueueDir(), maxAttempts)
		fmt.Printf("✓ Flush complete: sent=%d failed=%d dead_lettered=%d skipped=%v\n",
			result.Sent, result.Failed, result.DeadLettered, result.Skipped)
		if result.Failed > 0 || result.DeadLettered > 0 || len(errs) > 0 {
			return 1
		}
		return 0
	default:
		fmt.Println("Unknown queue subcommand:", args[0])
		return 1
	}
}

func backgroundReplayLoop(ctx context.Context, apiKey, baseURL string, intervalSec int) {
	if intervalSec <= 0 {
		intervalSec = defaultReplayInterval
	}
	if j := getReplayJitter(); j > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(j):
		}
	}
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()
	// Flush once after startup jitter, then on interval.
	_ = replayFailedPayloads(apiKey, baseURL, getQueueDir(), defaultMaxAttempts)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = replayFailedPayloads(apiKey, baseURL, getQueueDir(), defaultMaxAttempts)
		}
	}
}

func parseRunArgs(args []string) (runOptions, error) {
	opts := runOptions{
		captureLogs:       true,
		autoReplay:        true,
		replayIntervalSec: defaultReplayInterval,
	}
	if len(args) < 1 {
		return opts, fmt.Errorf("job name required")
	}
	opts.jobName = args[0]
	i := 1
	for ; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			i++
			break
		}
		if !strings.HasPrefix(arg, "--") {
			break
		}
		switch {
		case strings.HasPrefix(arg, "--capture-logs="):
			opts.captureLogs = strings.TrimPrefix(arg, "--capture-logs=") != "false"
		case strings.HasPrefix(arg, "--metadata="):
			raw := strings.TrimPrefix(arg, "--metadata=")
			if err := json.Unmarshal([]byte(raw), &opts.metadata); err != nil {
				return opts, fmt.Errorf("invalid --metadata JSON")
			}
		case strings.HasPrefix(arg, "--tags="):
			opts.tags = parseTags(strings.TrimPrefix(arg, "--tags="))
		case strings.HasPrefix(arg, "--base-url="):
			opts.baseURL = strings.TrimPrefix(arg, "--base-url=")
		case arg == "--no-auto-replay":
			opts.autoReplay = false
		case arg == "--background-replay":
			opts.backgroundReplay = true
		case strings.HasPrefix(arg, "--replay-interval="):
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--replay-interval="))
			if err != nil || n <= 0 {
				return opts, fmt.Errorf("invalid --replay-interval")
			}
			opts.replayIntervalSec = n
		default:
			return opts, fmt.Errorf("unknown flag: %s", arg)
		}
	}
	if i >= len(args) {
		return opts, fmt.Errorf("command required")
	}
	opts.command = args[i:]
	return opts, nil
}

func parseHeartbeatArgs(args []string) (heartbeatOptions, error) {
	opts := heartbeatOptions{autoReplay: true}
	if len(args) < 1 {
		return opts, fmt.Errorf("job name required")
	}
	opts.jobName = args[0]
	for _, arg := range args[1:] {
		switch {
		case strings.HasPrefix(arg, "--metadata="):
			raw := strings.TrimPrefix(arg, "--metadata=")
			if err := json.Unmarshal([]byte(raw), &opts.metadata); err != nil {
				return opts, fmt.Errorf("invalid --metadata JSON")
			}
		case strings.HasPrefix(arg, "--tags="):
			opts.tags = parseTags(strings.TrimPrefix(arg, "--tags="))
		case strings.HasPrefix(arg, "--base-url="):
			opts.baseURL = strings.TrimPrefix(arg, "--base-url=")
		case arg == "--no-auto-replay":
			opts.autoReplay = false
		default:
			return opts, fmt.Errorf("unknown flag: %s", arg)
		}
	}
	return opts, nil
}

func parseTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err == nil {
			return tags
		}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
