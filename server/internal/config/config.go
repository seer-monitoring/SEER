package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr                  string
	DBPath                    string
	APIKeys                   []string
	SlackWebhookURL           string
	SMTPHost                  string
	SMTPPort                  int
	SMTPUser                  string
	SMTPPass                  string
	SMTPFrom                  string
	SMTPTo                    string
	NotifyOnStart             bool
	NotifyOnSuccess           bool
	NotifyOnFailure           bool
	NotifyOnHeartbeatMissed   bool
	HeartbeatStaleAfterSec    int
	HeartbeatCheckIntervalSec int
	UIEnabled                 bool
	UISecret                  string
}

func Load() Config {
	cfg := Config{
		HTTPAddr:                  envOr("SEER_HTTP_ADDR", ":8080"),
		DBPath:                    envOr("SEER_DB_PATH", "data/seer.db"),
		SlackWebhookURL:           strings.TrimSpace(os.Getenv("SEER_SLACK_WEBHOOK_URL")),
		SMTPHost:                  strings.TrimSpace(os.Getenv("SEER_SMTP_HOST")),
		SMTPPort:                  envInt("SEER_SMTP_PORT", 587),
		SMTPUser:                  os.Getenv("SEER_SMTP_USER"),
		SMTPPass:                  os.Getenv("SEER_SMTP_PASS"),
		SMTPFrom:                  envOr("SEER_SMTP_FROM", os.Getenv("SEER_SMTP_USER")),
		SMTPTo:                    strings.TrimSpace(os.Getenv("SEER_SMTP_TO")),
		NotifyOnStart:             envBool("SEER_NOTIFY_ON_START", false),
		NotifyOnSuccess:           envBool("SEER_NOTIFY_ON_SUCCESS", false),
		NotifyOnFailure:           envBool("SEER_NOTIFY_ON_FAILURE", true),
		NotifyOnHeartbeatMissed:   envBool("SEER_NOTIFY_ON_HEARTBEAT_MISSED", true),
		HeartbeatStaleAfterSec:    envInt("SEER_HEARTBEAT_STALE_AFTER", 300),
		HeartbeatCheckIntervalSec: envIntAllowZero("SEER_HEARTBEAT_CHECK_INTERVAL", 0),
		UIEnabled:                 envBool("SEER_UI_ENABLED", true),
		UISecret:                  strings.TrimSpace(os.Getenv("SEER_UI_SECRET")),
	}
	raw := strings.TrimSpace(os.Getenv("SEER_API_KEYS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("SEER_API_KEY"))
	}
	if raw != "" {
		for _, part := range strings.Split(raw, ",") {
			key := strings.TrimSpace(part)
			if key != "" {
				cfg.APIKeys = append(cfg.APIKeys, key)
			}
		}
	}
	return cfg
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envIntAllowZero(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return fallback
	}
}
