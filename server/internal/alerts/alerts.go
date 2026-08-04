package alerts

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
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

type webhookPayload struct {
	Event        string `json:"event"`
	JobName      string `json:"job_name"`
	Status       string `json:"status"`
	RunID        string `json:"run_id,omitempty"`
	ErrorDetails string `json:"error_details,omitempty"`
	Subject      string `json:"subject"`
	Text         string `json:"text"`
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
		log.Printf("seer alert skipped: job=%q status=%q (notify flag off)", job.Name, status)
		return
	}

	runID := ""
	errDetails := ""
	if run != nil {
		runID = run.RunID
		errDetails = run.ErrorDetails
	}

	text := fmt.Sprintf(
		"Pipeline: %s\nStatus: %s\nRun ID: %s\nError Details: %s",
		job.Name,
		status,
		orNA(runID),
		orNone(errDetails),
	)
	subject := fmt.Sprintf("Job %s had a %s event.", job.Name, status)
	payload := webhookPayload{
		Event:        "job." + status,
		JobName:      job.Name,
		Status:       status,
		RunID:        runID,
		ErrorDetails: errDetails,
		Subject:      subject,
		Text:         text,
	}

	sent := 0

	if n.cfg.WebhookURL != "" {
		if err := n.sendWebhook(n.cfg.WebhookURL, payload); err != nil {
			log.Printf("webhook alert failed (%s): %v", n.cfg.WebhookURL, err)
		} else {
			sent++
			log.Printf("webhook alert sent: job=%q status=%q", job.Name, status)
		}
	}

	if n.cfg.SMTPHost != "" && n.cfg.SMTPTo != "" {
		if err := n.sendEmailTo(n.cfg.SMTPTo, subject, text); err != nil {
			log.Printf("email alert failed (to=%s host=%s:%d): %v", n.cfg.SMTPTo, n.cfg.SMTPHost, n.cfg.SMTPPort, err)
		} else {
			sent++
			log.Printf("email alert sent: job=%q status=%q to=%s", job.Name, status, n.cfg.SMTPTo)
		}
	}

	var channels []models.AlertChannel
	if err := n.db.Where("enabled = ?", true).Find(&channels).Error; err != nil {
		log.Printf("alert channels query failed: %v", err)
		return
	}
	for _, ch := range channels {
		typ := strings.ToLower(ch.Type)
		// Accept legacy "slack" rows as generic webhooks.
		if typ == "slack" {
			typ = "webhook"
		}
		switch typ {
		case "webhook":
			var cfg map[string]string
			_ = json.Unmarshal([]byte(ch.ConfigJSON), &cfg)
			url := firstNonEmpty(cfg["url"], cfg["webhook_url"])
			if url == "" {
				log.Printf("webhook channel %d skipped: missing url", ch.ID)
				continue
			}
			if err := n.sendWebhook(url, payload); err != nil {
				log.Printf("webhook channel %d failed: %v", ch.ID, err)
			} else {
				sent++
				log.Printf("webhook channel %d sent: job=%q status=%q", ch.ID, job.Name, status)
			}
		case "email":
			var cfg map[string]string
			_ = json.Unmarshal([]byte(ch.ConfigJSON), &cfg)
			to := cfg["to"]
			if to == "" {
				to = n.cfg.SMTPTo
			}
			if n.cfg.SMTPHost == "" || to == "" {
				log.Printf("email channel %d skipped: need SEER_SMTP_HOST and to=", ch.ID)
				continue
			}
			if err := n.sendEmailTo(to, subject, text); err != nil {
				log.Printf("email channel %d failed: %v", ch.ID, err)
			} else {
				sent++
				log.Printf("email channel %d sent: job=%q to=%s", ch.ID, job.Name, to)
			}
		default:
			log.Printf("alert channel %d skipped: unknown type %q", ch.ID, ch.Type)
		}
	}

	if sent == 0 {
		log.Printf("seer alert not delivered: job=%q status=%q — set SEER_WEBHOOK_URL and/or SEER_SMTP_* or add channels in /ui/channels",
			job.Name, status)
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

func (n *Notifier) sendWebhook(webhookURL string, payload webhookPayload) error {
	body, err := marshalWebhookBody(webhookURL, payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SEER/1.0 (+https://seer.ansrstudio.com)")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

func isDiscordWebhook(url string) bool {
	u := strings.ToLower(url)
	return strings.Contains(u, "discord.com/api/webhooks") ||
		strings.Contains(u, "discordapp.com/api/webhooks")
}

// marshalWebhookBody encodes a destination-appropriate JSON body.
// Discord requires "content" (or embeds); generic hooks get the structured payload
// (Slack-compatible "text" included).
func marshalWebhookBody(webhookURL string, payload webhookPayload) ([]byte, error) {
	if isDiscordWebhook(webhookURL) {
		content := payload.Text
		if content == "" {
			content = payload.Subject
		}
		if len(content) > 2000 {
			content = content[:1997] + "..."
		}
		return json.Marshal(map[string]string{"content": content})
	}
	return json.Marshal(payload)
}

func (n *Notifier) sendEmailTo(to, subject, text string) error {
	from := n.cfg.SMTPFrom
	if from == "" {
		from = n.cfg.SMTPUser
	}
	if from == "" {
		return fmt.Errorf("SEER_SMTP_FROM (or SEER_SMTP_USER) required")
	}

	msg := []byte("To: " + to + "\r\n" +
		"From: " + from + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" + text + "\r\n")

	addr := fmt.Sprintf("%s:%d", n.cfg.SMTPHost, n.cfg.SMTPPort)
	var auth smtp.Auth
	if n.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", n.cfg.SMTPUser, n.cfg.SMTPPass, n.cfg.SMTPHost)
	}

	// Port 465: implicit TLS. Otherwise prefer STARTTLS (587) when offered.
	if n.cfg.SMTPPort == 465 {
		return sendMailTLS(addr, n.cfg.SMTPHost, auth, from, []string{to}, msg)
	}
	return sendMailStartTLS(addr, n.cfg.SMTPHost, auth, from, []string{to}, msg)
}

func sendMailTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, tlsCfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	return smtpData(client, auth, from, to, msg)
}

func sendMailStartTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	return smtpData(client, auth, from, to, msg)
}

func smtpData(client *smtp.Client, auth smtp.Auth, from string, to []string, msg []byte) error {
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
