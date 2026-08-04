package alerts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/seer-monitoring/SEER/server/internal/config"
	"github.com/seer-monitoring/SEER/server/internal/models"
)

type Notifier struct {
	db     *gorm.DB
	cfg    config.Config
	client *http.Client
}

func New(db *gorm.DB, cfg config.Config) *Notifier {
	return &Notifier{
		db:  db,
		cfg: cfg,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Notify is best-effort — never returns an error to the caller path.
// status is one of: start, success, failed, cancelled, heartbeat.
func (n *Notifier) Notify(job models.Job, status string, run *models.Run) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("seer alerts panic: %v", r)
		}
	}()

	if !shouldNotify(job, status) {
		return
	}

	runID := ""
	errDetails := ""
	if run != nil {
		runID = run.RunID
		errDetails = run.ErrorDetails
	}

	displayStatus := status
	msg := fmt.Sprintf(
		"Pipeline: %s\nStatus: %s\nRun ID: %s\nError Details: %s",
		job.Name,
		displayStatus,
		orNA(runID),
		orNone(errDetails),
	)

	subject := fmt.Sprintf("Job %s had a %s event.", job.Name, displayStatus)

	if n.cfg.SlackWebhookURL != "" {
		if err := n.sendSlack(n.cfg.SlackWebhookURL, msg); err != nil {
			log.Printf("slack alert failed: %v", err)
		}
	}
	if n.cfg.SMTPHost != "" && n.cfg.SMTPTo != "" {
		if err := n.sendEmailTo(n.cfg.SMTPTo, subject, msg); err != nil {
			log.Printf("email alert failed: %v", err)
		}
	}

	var channels []models.AlertChannel
	if err := n.db.Where("enabled = ?", true).Find(&channels).Error; err != nil {
		return
	}
	for _, ch := range channels {
		switch strings.ToLower(ch.Type) {
		case "slack":
			var cfg map[string]string
			_ = json.Unmarshal([]byte(ch.ConfigJSON), &cfg)
			url := cfg["webhook_url"]
			if url == "" {
				continue
			}
			if err := n.sendSlack(url, msg); err != nil {
				log.Printf("slack channel alert failed: %v", err)
			}
		case "email":
			var cfg map[string]string
			_ = json.Unmarshal([]byte(ch.ConfigJSON), &cfg)
			to := cfg["to"]
			if to == "" {
				to = n.cfg.SMTPTo
			}
			if n.cfg.SMTPHost == "" || to == "" {
				continue
			}
			if err := n.sendEmailTo(to, subject, msg); err != nil {
				log.Printf("email channel alert failed: %v", err)
			}
		}
	}
}

func shouldNotify(job models.Job, status string) bool {
	switch status {
	case "start":
		return job.NotifyOnStart
	case "success":
		return job.NotifyOnSuccess
	case "failed", "cancelled":
		return job.NotifyOnFailure
	case "heartbeat":
		return job.NotifyOnHeartbeatMissed
	default:
		return false
	}
}

func (n *Notifier) sendSlack(webhookURL, text string) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	resp, err := n.client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack webhook status %d", resp.StatusCode)
	}
	return nil
}

func (n *Notifier) sendEmailTo(to, subject, text string) error {
	addr := fmt.Sprintf("%s:%d", n.cfg.SMTPHost, n.cfg.SMTPPort)
	from := n.cfg.SMTPFrom
	msg := []byte("To: " + to + "\r\n" +
		"From: " + from + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" + text + "\r\n")

	var auth smtp.Auth
	if n.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", n.cfg.SMTPUser, n.cfg.SMTPPass, n.cfg.SMTPHost)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, msg)
}

func orNA(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "None"
	}
	if len(s) > 2000 {
		return s[:2000] + "…"
	}
	return s
}
