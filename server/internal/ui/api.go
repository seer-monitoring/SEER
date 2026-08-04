package ui

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/seer-monitoring/SEER/server/internal/models"
)

type Handler struct {
	DB              *gorm.DB
	Session         *Session
	CheckHeartbeats func() (int, error)
}

type jobListItem struct {
	ID             uint       `json:"id"`
	Name           string     `json:"name"`
	LastStatus     string     `json:"last_status,omitempty"`
	LastRunID      string     `json:"last_run_id,omitempty"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	HeartbeatAt    *time.Time `json:"heartbeat_at,omitempty"`
	Stale          bool       `json:"stale"`
	StaleAfterSec  int        `json:"heartbeat_stale_after_sec"`
	NotifyOnStart  bool       `json:"notify_on_start"`
	NotifyOnSuccess bool      `json:"notify_on_success"`
	NotifyOnFailure bool      `json:"notify_on_failure"`
	NotifyOnHeartbeatMissed bool `json:"notify_on_heartbeat_missed"`
}

type runSummary struct {
	RunID        string     `json:"run_id"`
	Status       string     `json:"status"`
	StartTime    *time.Time `json:"start_time,omitempty"`
	EndTime      *time.Time `json:"end_time,omitempty"`
	ErrorDetails string     `json:"error_details,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (h *Handler) ListJobs(c *fiber.Ctx) error {
	var jobs []models.Job
	if err := h.DB.Order("name asc").Find(&jobs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	now := time.Now().UTC()
	out := make([]jobListItem, 0, len(jobs))
	for _, job := range jobs {
		item := h.toJobListItem(job, now)
		out = append(out, item)
	}
	return c.JSON(fiber.Map{"jobs": out})
}

func (h *Handler) GetJob(c *fiber.Ctx) error {
	name := strings.TrimSpace(c.Params("name"))
	var job models.Job
	if err := h.DB.Where("name = ?", name).First(&job).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "job not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	item := h.toJobListItem(job, time.Now().UTC())

	var runs []models.Run
	_ = h.DB.Where("job_id = ?", job.ID).Order("id desc").Limit(50).Find(&runs)
	summaries := make([]runSummary, 0, len(runs))
	for _, r := range runs {
		summaries = append(summaries, runSummary{
			RunID:        r.RunID,
			Status:       r.Status,
			StartTime:    r.StartTime,
			EndTime:      r.EndTime,
			ErrorDetails: truncate(r.ErrorDetails, 200),
			CreatedAt:    r.CreatedAt,
		})
	}

	var hb *models.Heartbeat
	var row models.Heartbeat
	if err := h.DB.Where("job_id = ?", job.ID).First(&row).Error; err == nil {
		hb = &row
	}

	return c.JSON(fiber.Map{
		"job":       item,
		"runs":      summaries,
		"heartbeat": hb,
	})
}

type patchJobRequest struct {
	NotifyOnStart           *bool `json:"notify_on_start"`
	NotifyOnSuccess         *bool `json:"notify_on_success"`
	NotifyOnFailure         *bool `json:"notify_on_failure"`
	NotifyOnHeartbeatMissed *bool `json:"notify_on_heartbeat_missed"`
	HeartbeatStaleAfterSec  *int  `json:"heartbeat_stale_after_sec"`
}

func (h *Handler) PatchJob(c *fiber.Ctx) error {
	name := strings.TrimSpace(c.Params("name"))
	var job models.Job
	if err := h.DB.Where("name = ?", name).First(&job).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "job not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	var req patchJobRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid json"})
	}
	updates := map[string]any{}
	if req.NotifyOnStart != nil {
		updates["notify_on_start"] = *req.NotifyOnStart
	}
	if req.NotifyOnSuccess != nil {
		updates["notify_on_success"] = *req.NotifyOnSuccess
	}
	if req.NotifyOnFailure != nil {
		updates["notify_on_failure"] = *req.NotifyOnFailure
	}
	if req.NotifyOnHeartbeatMissed != nil {
		updates["notify_on_heartbeat_missed"] = *req.NotifyOnHeartbeatMissed
	}
	if req.HeartbeatStaleAfterSec != nil {
		if *req.HeartbeatStaleAfterSec < 1 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "heartbeat_stale_after_sec must be >= 1"})
		}
		updates["heartbeat_stale_after_sec"] = *req.HeartbeatStaleAfterSec
	}
	if len(updates) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "no fields to update"})
	}
	if err := h.DB.Model(&job).Updates(updates).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	_ = h.DB.Where("name = ?", name).First(&job)
	return c.JSON(fiber.Map{"job": h.toJobListItem(job, time.Now().UTC())})
}

func (h *Handler) GetRun(c *fiber.Ctx) error {
	runID := strings.TrimSpace(c.Params("run_id"))
	var run models.Run
	if err := h.DB.Where("run_id = ?", runID).First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "run not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	var job models.Job
	_ = h.DB.First(&job, run.JobID)
	return c.JSON(fiber.Map{
		"run":      run,
		"job_name": job.Name,
		"metadata": parseJSONMaybe(run.MetadataJSON),
		"tags":     parseJSONMaybe(run.TagsJSON),
	})
}

func (h *Handler) ListChannels(c *fiber.Ctx) error {
	var channels []models.AlertChannel
	if err := h.DB.Order("id asc").Find(&channels).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Migrate legacy Slack channel rows to generic webhook.
	for i := range channels {
		ch := &channels[i]
		if strings.EqualFold(ch.Type, "slack") {
			ch.Type = "webhook"
			cfg := normalizeWebhookConfigJSON(ch.ConfigJSON)
			_ = h.DB.Model(ch).Updates(map[string]any{"type": "webhook", "config_json": cfg}).Error
			ch.ConfigJSON = cfg
		} else if strings.EqualFold(ch.Type, "webhook") {
			cfg := normalizeWebhookConfigJSON(ch.ConfigJSON)
			if cfg != ch.ConfigJSON {
				_ = h.DB.Model(ch).Update("config_json", cfg).Error
				ch.ConfigJSON = cfg
			}
		}
	}
	return c.JSON(fiber.Map{"channels": channels})
}

func normalizeWebhookConfigJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	if _, ok := m["url"]; !ok {
		if wu, ok := m["webhook_url"]; ok {
			m["url"] = wu
			delete(m, "webhook_url")
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

type channelRequest struct {
	Type    string          `json:"type"`
	Enabled *bool           `json:"enabled"`
	Config  json.RawMessage `json:"config"`
}

func (h *Handler) CreateChannel(c *fiber.Ctx) error {
	var req channelRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid json"})
	}
	typ := strings.ToLower(strings.TrimSpace(req.Type))
	if typ == "slack" {
		typ = "webhook"
	}
	if typ != "webhook" && typ != "email" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "type must be webhook or email"})
	}
	cfg := strings.TrimSpace(string(req.Config))
	if cfg == "" || cfg == "null" {
		cfg = "{}"
	}
	if !json.Valid([]byte(cfg)) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "config must be JSON object"})
	}
	if typ == "webhook" {
		cfg = normalizeWebhookConfigJSON(cfg)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	ch := models.AlertChannel{Type: typ, ConfigJSON: cfg, Enabled: enabled}
	if err := h.DB.Create(&ch).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"channel": ch})
}

func (h *Handler) PatchChannel(c *fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}
	var ch models.AlertChannel
	if err := h.DB.First(&ch, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "channel not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	var req channelRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid json"})
	}
	updates := map[string]any{}
	if req.Type != "" {
		typ := strings.ToLower(strings.TrimSpace(req.Type))
		if typ == "slack" {
			typ = "webhook"
		}
		if typ != "webhook" && typ != "email" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "type must be webhook or email"})
		}
		updates["type"] = typ
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if len(req.Config) > 0 && string(req.Config) != "null" {
		if !json.Valid(req.Config) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "config must be JSON"})
		}
		cfg := string(req.Config)
		typ := ch.Type
		if v, ok := updates["type"].(string); ok {
			typ = v
		}
		if strings.EqualFold(typ, "webhook") {
			cfg = normalizeWebhookConfigJSON(cfg)
		}
		updates["config_json"] = cfg
	}
	if len(updates) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "no fields to update"})
	}
	if err := h.DB.Model(&ch).Updates(updates).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	_ = h.DB.First(&ch, id)
	return c.JSON(fiber.Map{"channel": ch})
}

func (h *Handler) DeleteChannel(c *fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}
	res := h.DB.Delete(&models.AlertChannel{}, id)
	if res.Error != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": res.Error.Error()})
	}
	if res.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "channel not found"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) CheckHeartbeat(c *fiber.Ctx) error {
	if h.CheckHeartbeats == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "heartbeat check unavailable"})
	}
	n, err := h.CheckHeartbeats()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"alerted": n})
}

func (h *Handler) toJobListItem(job models.Job, now time.Time) jobListItem {
	item := jobListItem{
		ID:                      job.ID,
		Name:                    job.Name,
		StaleAfterSec:           job.HeartbeatStaleAfterSec,
		NotifyOnStart:           job.NotifyOnStart,
		NotifyOnSuccess:         job.NotifyOnSuccess,
		NotifyOnFailure:         job.NotifyOnFailure,
		NotifyOnHeartbeatMissed: job.NotifyOnHeartbeatMissed,
	}
	var run models.Run
	if err := h.DB.Where("job_id = ?", job.ID).Order("id desc").First(&run).Error; err == nil {
		item.LastStatus = run.Status
		item.LastRunID = run.RunID
		if run.EndTime != nil {
			item.LastRunAt = run.EndTime
		} else {
			item.LastRunAt = run.StartTime
		}
	}
	var hb models.Heartbeat
	if err := h.DB.Where("job_id = ?", job.ID).First(&hb).Error; err == nil {
		t := hb.SeenAt
		item.HeartbeatAt = &t
		staleSec := job.HeartbeatStaleAfterSec
		if staleSec <= 0 {
			staleSec = 300
		}
		item.Stale = hb.SeenAt.Before(now.Add(-time.Duration(staleSec) * time.Second))
	}
	return item
}

func parseJSONMaybe(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
