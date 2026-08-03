package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultBaseURL        = "https://api.ansrstudio.com"
	defaultMaxAttempts    = 5
	defaultMaxQueueFiles  = 500
	defaultMaxQueueBytes  = 50 * 1024 * 1024 // 50 MiB
	defaultTimeoutSec     = 30
	defaultReplayInterval = 60
	envelopeVersion       = 3
	maxLogBytes           = 200_000
)

func resolveBaseURL(explicit string) string {
	if explicit != "" {
		return strings.TrimRight(explicit, "/")
	}
	if env := strings.TrimSpace(os.Getenv("SEER_BASE_URL")); env != "" {
		return strings.TrimRight(env, "/")
	}
	return defaultBaseURL
}

func resolveAPIKey() string {
	if key := os.Getenv("SEER_API_KEY"); key != "" {
		return key
	}
	return os.Getenv("SEER_APIKEY")
}

func getQueueDir() string {
	if override := os.Getenv("SEER_QUEUE_DIR"); override != "" {
		return filepath.Clean(override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".seer", "queue")
	}
	return filepath.Join(home, ".seer", "queue")
}

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getQueueLimits() (maxFiles, maxBytes int) {
	return envInt("SEER_QUEUE_MAX_FILES", defaultMaxQueueFiles),
		envInt("SEER_QUEUE_MAX_BYTES", defaultMaxQueueBytes)
}

func getTimeout() int {
	return envInt("SEER_TIMEOUT", defaultTimeoutSec)
}
