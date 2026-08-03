package api

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/seer-monitoring/SEER/server/internal/alerts"
	"github.com/seer-monitoring/SEER/server/internal/models"
)

type Server struct {
	DB       *gorm.DB
	Notifier *alerts.Notifier
}

type monitoringRequest struct {
	JobName      string          `json:"job_name"`
	Status       string          `json:"status"`
	RunID        string          `json:"run_id"`
	StartTime    *string         `json:"start_time"`
	EndTime      *string         `json:"end_time"`
	Metadata     json.RawMessage `json:"metadata"`
	ErrorDetails *string         `json:"error_details"`
	Tags         json.RawMessage `json:"tags"`
	Logs         *string         `json:"logs"`
}

type heartbeatRequest struct {
	JobName     string          `json:"job_name"`
	CurrentTime *string         `json:"current_time"`
	Metadata    json.RawMessage `json:"metadata"`
	Tags        json.RawMessage `json:"tags"`
}

func (s *Server) Health(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok", "edition": "community"})
}

func (s *Server) Monitoring(c *fiber.Ctx) error {
	var req monitoringRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid json"})
	}
	req.JobName = strings.TrimSpace(req.JobName)
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	if req.JobName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "job_name required"})
	}
	if req.Status == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status required"})
	}

	idem := strings.TrimSpace(c.Get("Idempotency-Key"))
	idemBase := strings.TrimSuffix(strings.TrimSuffix(idem, ":register"), ":complete")

	job, err := s.ensureJob(req.JobName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	switch req.Status {
	case "running":
		return s.handleRunning(c, job, req, idemBase)
	case "success", "failed":
		return s.handleTerminal(c, job, req, idemBase)
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unsupported status"})
	}
}

func (s *Server) handleRunning(c *fiber.Ctx, job models.Job, req monitoringRequest, idemBase string) error {
	if idemBase != "" {
		var existing models.Run
		err := s.DB.Where("idempotency_key = ? AND status = ?", idemBase, "running").First(&existing).Error
		if err == nil {
			return c.JSON(fiber.Map{"run_id": existing.RunID, "status": existing.Status})
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
	}

	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = uuid.NewString()
	}

	start := parseFlexibleTime(req.StartTime)
	run := models.Run{
		JobID:          job.ID,
		RunID:          runID,
		Status:         "running",
		StartTime:      start,
		MetadataJSON:   rawOrEmpty(req.Metadata),
		TagsJSON:       rawOrEmpty(req.Tags),
		IdempotencyKey: idemBase,
	}
	if err := s.DB.Create(&run).Error; err != nil {
		// Unique run_id race — return existing
		var existing models.Run
		if s.DB.Where("run_id = ?", runID).First(&existing).Error == nil {
			return c.JSON(fiber.Map{"run_id": existing.RunID, "status": existing.Status})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"run_id": run.RunID, "status": run.Status})
}

func (s *Server) handleTerminal(c *fiber.Ctx, job models.Job, req monitoringRequest, idemBase string) error {
	if idemBase != "" {
		var existing models.Run
		err := s.DB.Where("idempotency_key = ? AND status IN ?", idemBase, []string{"success", "failed"}).First(&existing).Error
		if err == nil {
			return c.JSON(fiber.Map{"run_id": existing.RunID, "status": existing.Status})
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
	}

	runID := strings.TrimSpace(req.RunID)
	var run models.Run
	now := time.Now().UTC()
	end := parseFlexibleTime(req.EndTime)
	if end == nil {
		end = &now
	}
	start := parseFlexibleTime(req.StartTime)
	errDetails := ""
	if req.ErrorDetails != nil {
		errDetails = *req.ErrorDetails
	}
	logs := ""
	if req.Logs != nil {
		logs = *req.Logs
	}

	if runID != "" {
		err := s.DB.Where("run_id = ?", runID).First(&run).Error
		if err == nil {
			run.Status = req.Status
			run.EndTime = end
			if start != nil {
				run.StartTime = start
			}
			run.MetadataJSON = rawOrEmpty(req.Metadata)
			run.TagsJSON = rawOrEmpty(req.Tags)
			run.Logs = logs
			run.ErrorDetails = errDetails
			if idemBase != "" {
				run.IdempotencyKey = idemBase
			}
			if err := s.DB.Save(&run).Error; err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
			}
			if req.Status == "failed" && s.Notifier != nil {
				go s.Notifier.NotifyFailed(job.Name, &run)
			}
			return c.JSON(fiber.Map{"run_id": run.RunID, "status": run.Status})
		}
		if err != gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
	}

	if runID == "" {
		runID = uuid.NewString()
	}
	run = models.Run{
		JobID:          job.ID,
		RunID:          runID,
		Status:         req.Status,
		StartTime:      start,
		EndTime:        end,
		MetadataJSON:   rawOrEmpty(req.Metadata),
		TagsJSON:       rawOrEmpty(req.Tags),
		Logs:           logs,
		ErrorDetails:   errDetails,
		IdempotencyKey: idemBase,
	}
	if err := s.DB.Create(&run).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if req.Status == "failed" && s.Notifier != nil {
		go s.Notifier.NotifyFailed(job.Name, &run)
	}
	return c.JSON(fiber.Map{"run_id": run.RunID, "status": run.Status})
}

func (s *Server) Heartbeat(c *fiber.Ctx) error {
	var req heartbeatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid json"})
	}
	req.JobName = strings.TrimSpace(req.JobName)
	if req.JobName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "job_name required"})
	}
	job, err := s.ensureJob(req.JobName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	seen := parseFlexibleTime(req.CurrentTime)
	if seen == nil {
		now := time.Now().UTC()
		seen = &now
	}

	var hb models.Heartbeat
	err = s.DB.Where("job_id = ?", job.ID).First(&hb).Error
	if err == gorm.ErrRecordNotFound {
		hb = models.Heartbeat{
			JobID:        job.ID,
			SeenAt:       *seen,
			MetadataJSON: rawOrEmpty(req.Metadata),
			TagsJSON:     rawOrEmpty(req.Tags),
		}
		if err := s.DB.Create(&hb).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
	} else if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	} else {
		hb.SeenAt = *seen
		hb.MetadataJSON = rawOrEmpty(req.Metadata)
		hb.TagsJSON = rawOrEmpty(req.Tags)
		if err := s.DB.Save(&hb).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
	}
	return c.JSON(fiber.Map{"ok": true, "job_name": job.Name, "seen_at": hb.SeenAt})
}

func (s *Server) EnterpriseStub(c *fiber.Ctx) error {
	feature := c.Params("feature", "unknown")
	return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
		"error":   "enterprise_required",
		"edition": "community",
		"feature": feature,
		"message": "This feature requires Seer Enterprise (RBAC, SSO, PagerDuty/Datadog sync, audit logging).",
	})
}

func (s *Server) ensureJob(name string) (models.Job, error) {
	var job models.Job
	err := s.DB.Where("name = ?", name).First(&job).Error
	if err == nil {
		return job, nil
	}
	if err != gorm.ErrRecordNotFound {
		return job, err
	}
	job = models.Job{Name: name}
	if err := s.DB.Create(&job).Error; err != nil {
		// race on unique name
		if s.DB.Where("name = ?", name).First(&job).Error == nil {
			return job, nil
		}
		return job, err
	}
	return job, nil
}

func rawOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}

func parseFlexibleTime(raw *string) *time.Time {
	if raw == nil {
		return nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05+00:00",
		"2006-01-02T15:04:05.999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			utc := t.UTC()
			return &utc
		}
	}
	return nil
}
