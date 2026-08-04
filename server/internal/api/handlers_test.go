package api_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/seer-monitoring/SEER/server/internal/api"
	"github.com/seer-monitoring/SEER/server/internal/auth"
	"github.com/seer-monitoring/SEER/server/internal/config"
	"github.com/seer-monitoring/SEER/server/internal/db"
	"github.com/seer-monitoring/SEER/server/internal/models"
	"gorm.io/gorm"
)

type spyNotifier struct {
	mu     sync.Mutex
	events []spyEvent
}

type spyEvent struct {
	JobName string
	Status  string
	RunID   string
}

func (s *spyNotifier) Notify(job models.Job, status string, run *models.Run) {
	if !spyShouldNotify(job, status) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runID := ""
	if run != nil {
		runID = run.RunID
	}
	s.events = append(s.events, spyEvent{JobName: job.Name, Status: status, RunID: runID})
}

func spyShouldNotify(job models.Job, status string) bool {
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

func (s *spyNotifier) statuses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.events))
	for i, e := range s.events {
		out[i] = e.Status
	}
	return out
}

func (s *spyNotifier) waitFor(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := len(s.events)
		s.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

type testEnv struct {
	app      *fiber.App
	db       *gorm.DB
	notifier *spyNotifier
	cfg      config.Config
}

func setupEnv(t *testing.T, cfg config.Config) *testEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	gdb, err := db.Open(path)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close(gdb)
	})
	if cfg.HeartbeatStaleAfterSec == 0 {
		cfg.HeartbeatStaleAfterSec = 300
	}
	spy := &spyNotifier{}
	srv := &api.Server{DB: gdb, Notifier: spy, Cfg: cfg}
	app := fiber.New()
	app.Get("/health", srv.Health)
	authMW := auth.Middleware([]string{"test-key"})
	app.Post("/monitoring", authMW, srv.Monitoring)
	app.Post("/heartbeat", authMW, srv.Heartbeat)
	app.Get("/check_heartbeat", authMW, srv.CheckHeartbeat)
	ent := app.Group("/enterprise", authMW)
	ent.All("/:feature", srv.EnterpriseStub)
	return &testEnv{app: app, db: gdb, notifier: spy, cfg: cfg}
}

func setupApp(t *testing.T) *fiber.App {
	t.Helper()
	return setupEnv(t, config.Config{
		NotifyOnFailure:         true,
		NotifyOnHeartbeatMissed: true,
		HeartbeatStaleAfterSec:  300,
	}).app
}

func postJSON(t *testing.T, app *fiber.App, path, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Authorization", "test-key")
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func TestHealth(t *testing.T) {
	app := setupApp(t)
	req := httptest.NewRequest("GET", "/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestMonitoringRegisterAndComplete(t *testing.T) {
	env := setupEnv(t, config.Config{NotifyOnFailure: true, HeartbeatStaleAfterSec: 300})

	status, start := postJSON(t, env.app, "/monitoring",
		`{"job_name":"job1","status":"running","run_id":"","start_time":"2026-01-01T00:00:00Z"}`,
		map[string]string{"Idempotency-Key": "k1:register"})
	if status != 200 {
		t.Fatalf("status=%d body=%v", status, start)
	}
	runID, _ := start["run_id"].(string)
	if runID == "" {
		t.Fatal("expected run_id")
	}

	status, _ = postJSON(t, env.app, "/monitoring",
		`{"job_name":"job1","status":"success","run_id":"`+runID+`","start_time":"2026-01-01T00:00:00Z","end_time":"2026-01-01T00:01:00Z"}`,
		map[string]string{"Idempotency-Key": "k1:complete"})
	if status != 200 {
		t.Fatalf("complete status=%d", status)
	}
}

func TestProgressUpsertNoAlert(t *testing.T) {
	env := setupEnv(t, config.Config{
		NotifyOnStart:          true,
		NotifyOnFailure:        true,
		HeartbeatStaleAfterSec: 300,
	})

	status, start := postJSON(t, env.app, "/monitoring",
		`{"job_name":"job1","status":"running"}`,
		map[string]string{"Idempotency-Key": "p1:register"})
	if status != 200 {
		t.Fatalf("start status=%d", status)
	}
	runID := start["run_id"].(string)
	if !env.notifier.waitFor(1, time.Second) {
		t.Fatal("expected start notify")
	}

	status, prog := postJSON(t, env.app, "/monitoring",
		`{"job_name":"job1","status":"running","run_id":"`+runID+`","metadata":{"step":2},"logs":"halfway"}`,
		nil)
	if status != 200 {
		t.Fatalf("progress status=%d body=%v", status, prog)
	}
	if prog["update_status"] != "Success" {
		t.Fatalf("update_status=%v", prog["update_status"])
	}

	time.Sleep(30 * time.Millisecond)
	if got := env.notifier.statuses(); len(got) != 1 || got[0] != "start" {
		t.Fatalf("expected only start notify, got %v", got)
	}

	var run models.Run
	if err := env.db.Where("run_id = ?", runID).First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Logs != "halfway" {
		t.Fatalf("logs=%q", run.Logs)
	}
	if run.MetadataJSON != `{"step":2}` {
		t.Fatalf("metadata=%q", run.MetadataJSON)
	}
}

func TestCancelledStatusAndNotify(t *testing.T) {
	env := setupEnv(t, config.Config{
		NotifyOnFailure:        true,
		HeartbeatStaleAfterSec: 300,
	})

	status, start := postJSON(t, env.app, "/monitoring",
		`{"job_name":"job1","status":"running"}`, nil)
	if status != 200 {
		t.Fatalf("start=%d", status)
	}
	runID := start["run_id"].(string)

	status, done := postJSON(t, env.app, "/monitoring",
		`{"job_name":"job1","status":"cancelled","run_id":"`+runID+`","error_details":"user abort"}`,
		map[string]string{"Idempotency-Key": "c1:complete"})
	if status != 200 {
		t.Fatalf("cancel status=%d", status)
	}
	if done["status"] != "cancelled" {
		t.Fatalf("status=%v", done["status"])
	}
	if !env.notifier.waitFor(1, time.Second) {
		t.Fatal("expected cancelled notify")
	}
	if got := env.notifier.statuses(); len(got) != 1 || got[0] != "cancelled" {
		t.Fatalf("events=%v", got)
	}

	status, again := postJSON(t, env.app, "/monitoring",
		`{"job_name":"job1","status":"cancelled","run_id":"`+runID+`"}`,
		map[string]string{"Idempotency-Key": "c1:complete"})
	if status != 200 {
		t.Fatalf("retry status=%d", status)
	}
	if again["run_id"] != runID {
		t.Fatalf("run_id=%v", again["run_id"])
	}
	time.Sleep(30 * time.Millisecond)
	if len(env.notifier.statuses()) != 1 {
		t.Fatalf("idempotent should not re-notify, got %v", env.notifier.statuses())
	}
}

func TestNotifyGates(t *testing.T) {
	env := setupEnv(t, config.Config{
		NotifyOnStart:          false,
		NotifyOnSuccess:        true,
		NotifyOnFailure:        false,
		HeartbeatStaleAfterSec: 300,
	})

	status, start := postJSON(t, env.app, "/monitoring",
		`{"job_name":"gated","status":"running"}`, nil)
	if status != 200 {
		t.Fatalf("start=%d", status)
	}
	runID := start["run_id"].(string)
	time.Sleep(30 * time.Millisecond)
	if len(env.notifier.statuses()) != 0 {
		t.Fatalf("start should not notify, got %v", env.notifier.statuses())
	}

	status, _ = postJSON(t, env.app, "/monitoring",
		`{"job_name":"gated","status":"success","run_id":"`+runID+`"}`, nil)
	if status != 200 {
		t.Fatalf("success=%d", status)
	}
	if !env.notifier.waitFor(1, time.Second) {
		t.Fatal("expected success notify")
	}
	if got := env.notifier.statuses(); got[0] != "success" {
		t.Fatalf("events=%v", got)
	}
}

func TestUnauthorized(t *testing.T) {
	app := setupApp(t)
	req := httptest.NewRequest("POST", "/monitoring", strings.NewReader(`{"job_name":"x","status":"running"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestEnterpriseStub(t *testing.T) {
	app := setupApp(t)
	req := httptest.NewRequest("GET", "/enterprise/rbac", nil)
	req.Header.Set("Authorization", "test-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 402 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHeartbeat(t *testing.T) {
	app := setupApp(t)
	status, body := postJSON(t, app, "/heartbeat", `{"job_name":"worker","metadata":{"pid":1}}`, nil)
	if status != 200 {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestHeartbeatMissCheckAndDebounce(t *testing.T) {
	env := setupEnv(t, config.Config{
		NotifyOnHeartbeatMissed: true,
		HeartbeatStaleAfterSec:  60,
	})

	status, _ := postJSON(t, env.app, "/heartbeat", `{"job_name":"worker"}`, nil)
	if status != 200 {
		t.Fatalf("heartbeat=%d", status)
	}

	var job models.Job
	if err := env.db.Where("name = ?", "worker").First(&job).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Minute)
	if err := env.db.Model(&models.Heartbeat{}).Where("job_id = ?", job.ID).Update("seen_at", old).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/check_heartbeat", nil)
	req.Header.Set("Authorization", "test-key")
	resp, err := env.app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("check status=%d", resp.StatusCode)
	}
	if !env.notifier.waitFor(1, time.Second) {
		t.Fatal("expected miss alert")
	}
	if got := env.notifier.statuses(); got[0] != "heartbeat" {
		t.Fatalf("events=%v", got)
	}

	resp2, err := env.app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != 200 {
		t.Fatalf("check2 status=%d", resp2.StatusCode)
	}
	time.Sleep(30 * time.Millisecond)
	if len(env.notifier.statuses()) != 1 {
		t.Fatalf("debounced expected 1 alert, got %v", env.notifier.statuses())
	}

	status, _ = postJSON(t, env.app, "/heartbeat", `{"job_name":"worker"}`, nil)
	if status != 200 {
		t.Fatalf("fresh hb=%d", status)
	}
	if err := env.db.Where("name = ?", "worker").First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.LastMissAlertAt != nil {
		t.Fatal("expected LastMissAlertAt cleared")
	}
}
