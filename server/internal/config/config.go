package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr       string
	DBPath         string
	APIKeys        []string
	SlackWebhookURL string
	SMTPHost       string
	SMTPPort       int
	SMTPUser       string
	SMTPPass       string
	SMTPFrom       string
	SMTPTo         string
}

func Load() Config {
	cfg := Config{
		HTTPAddr:        envOr("SEER_HTTP_ADDR", ":8080"),
		DBPath:          envOr("SEER_DB_PATH", "data/seer.db"),
		SlackWebhookURL: strings.TrimSpace(os.Getenv("SEER_SLACK_WEBHOOK_URL")),
		SMTPHost:        strings.TrimSpace(os.Getenv("SEER_SMTP_HOST")),
		SMTPPort:        envInt("SEER_SMTP_PORT", 587),
		SMTPUser:        os.Getenv("SEER_SMTP_USER"),
		SMTPPass:        os.Getenv("SEER_SMTP_PASS"),
		SMTPFrom:        envOr("SEER_SMTP_FROM", os.Getenv("SEER_SMTP_USER")),
		SMTPTo:          strings.TrimSpace(os.Getenv("SEER_SMTP_TO")),
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
