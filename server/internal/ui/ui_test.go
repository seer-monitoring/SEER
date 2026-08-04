package ui_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/seer-monitoring/SEER/server/internal/db"
	"github.com/seer-monitoring/SEER/server/internal/models"
	"github.com/seer-monitoring/SEER/server/internal/ui"
)

func setupUI(t *testing.T) (*fiber.App, *ui.Handler) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ui.db")
	gdb, err := db.Open(path)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(gdb) })

	sess := ui.NewSession("test-ui-secret", []string{"test-key"})
	h := &ui.Handler{
		DB:      gdb,
		Session: sess,
		CheckHeartbeats: func() (int, error) {
			return 0, nil
		},
	}
	app := fiber.New()
	ui.Mount(app, h)
	return app, h
}

func loginCookie(t *testing.T, app *fiber.App) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/login", bytes.NewBufferString("api_key=test-key&next=/ui/"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "seer_ui_session" && c.Value != "" {
			return c.Value
		}
	}
	t.Fatal("missing session cookie")
	return ""
}

func apiReq(t *testing.T, app *fiber.App, method, path, cookie, body string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, r)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "seer_ui_session", Value: cookie})
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
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

func TestUIUnauthorized(t *testing.T) {
	app, _ := setupUI(t)
	status, body := apiReq(t, app, http.MethodGet, "/api/ui/jobs", "", "")
	if status != 401 {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestUIJobsPatchAndChannels(t *testing.T) {
	app, h := setupUI(t)
	cookie := loginCookie(t, app)

	job := models.Job{
		Name:                   "demo",
		NotifyOnFailure:        true,
		HeartbeatStaleAfterSec: 300,
	}
	if err := h.DB.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	run := models.Run{
		JobID:  job.ID,
		RunID:  "run-1",
		Status: "success",
		Logs:   "ok",
	}
	if err := h.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	seen := time.Now().UTC()
	if err := h.DB.Create(&models.Heartbeat{JobID: job.ID, SeenAt: seen}).Error; err != nil {
		t.Fatal(err)
	}

	status, body := apiReq(t, app, http.MethodGet, "/api/ui/jobs", cookie, "")
	if status != 200 {
		t.Fatalf("list jobs status=%d body=%v", status, body)
	}
	jobs, _ := body["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("jobs=%v", body["jobs"])
	}

	status, body = apiReq(t, app, http.MethodPatch, "/api/ui/jobs/demo", cookie,
		`{"notify_on_start":true,"notify_on_success":true,"heartbeat_stale_after_sec":120}`)
	if status != 200 {
		t.Fatalf("patch status=%d body=%v", status, body)
	}
	jobOut, _ := body["job"].(map[string]any)
	if jobOut["notify_on_start"] != true {
		t.Fatalf("job=%v", jobOut)
	}
	if int(jobOut["heartbeat_stale_after_sec"].(float64)) != 120 {
		t.Fatalf("stale=%v", jobOut["heartbeat_stale_after_sec"])
	}

	status, body = apiReq(t, app, http.MethodGet, "/api/ui/runs/run-1", cookie, "")
	if status != 200 {
		t.Fatalf("run status=%d body=%v", status, body)
	}
	if body["job_name"] != "demo" {
		t.Fatalf("job_name=%v", body["job_name"])
	}

	status, body = apiReq(t, app, http.MethodPost, "/api/ui/channels", cookie,
		`{"type":"webhook","enabled":true,"config":{"url":"https://hooks.example/x"}}`)
	if status != 201 {
		t.Fatalf("create channel status=%d body=%v", status, body)
	}
	ch, _ := body["channel"].(map[string]any)
	if ch["type"] != "webhook" {
		t.Fatalf("type=%v", ch["type"])
	}
	id := int(ch["id"].(float64))

	// Legacy Slack create should normalize to webhook + url key.
	status, body = apiReq(t, app, http.MethodPost, "/api/ui/channels", cookie,
		`{"type":"slack","enabled":true,"config":{"webhook_url":"https://hooks.example/legacy"}}`)
	if status != 201 {
		t.Fatalf("legacy create status=%d body=%v", status, body)
	}
	legacy, _ := body["channel"].(map[string]any)
	if legacy["type"] != "webhook" {
		t.Fatalf("legacy type=%v", legacy["type"])
	}
	if cfg, _ := legacy["config"].(string); !strings.Contains(cfg, `"url"`) || strings.Contains(cfg, "webhook_url") {
		t.Fatalf("legacy config=%v", legacy["config"])
	}
	legacyID := int(legacy["id"].(float64))

	// Seed a raw slack row and ensure list migrates it.
	if err := h.DB.Create(&models.AlertChannel{
		Type:       "slack",
		ConfigJSON: `{"webhook_url":"https://hooks.example/db"}`,
		Enabled:    true,
	}).Error; err != nil {
		t.Fatalf("seed slack: %v", err)
	}
	status, body = apiReq(t, app, http.MethodGet, "/api/ui/channels", cookie, "")
	if status != 200 {
		t.Fatalf("list channels status=%d body=%v", status, body)
	}
	listed, _ := body["channels"].([]any)
	for _, raw := range listed {
		row, _ := raw.(map[string]any)
		if row["type"] == "slack" {
			t.Fatalf("list still returned slack: %v", row)
		}
	}

	status, body = apiReq(t, app, http.MethodPatch, "/api/ui/channels/"+strconv.Itoa(id), cookie, `{"enabled":false}`)
	if status != 200 {
		t.Fatalf("patch channel status=%d body=%v", status, body)
	}
	ch, _ = body["channel"].(map[string]any)
	if ch["enabled"] != false {
		t.Fatalf("enabled=%v", ch["enabled"])
	}

	status, body = apiReq(t, app, http.MethodDelete, "/api/ui/channels/"+strconv.Itoa(id), cookie, "")
	if status != 200 {
		t.Fatalf("delete status=%d body=%v", status, body)
	}
	_, _ = apiReq(t, app, http.MethodDelete, "/api/ui/channels/"+strconv.Itoa(legacyID), cookie, "")

	status, body = apiReq(t, app, http.MethodPost, "/api/ui/check_heartbeat", cookie, "")
	if status != 200 {
		t.Fatalf("check hb status=%d body=%v", status, body)
	}
}

func TestUILoginPage(t *testing.T) {
	app, _ := setupUI(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/login", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(raw, []byte("SEER")) {
		t.Fatalf("expected SEER branding in login html")
	}
}

func TestUILoginNotBlockedByIngestAuth(t *testing.T) {
	// Reproduce production wiring: ingest auth must not wrap /ui.
	path := filepath.Join(t.TempDir(), "ui2.db")
	gdb, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(gdb) })

	app := fiber.New()
	authMW := func(c *fiber.Ctx) error {
		if c.Get("Authorization") == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing Authorization"})
		}
		return c.Next()
	}
	app.Post("/monitoring", authMW, func(c *fiber.Ctx) error { return c.SendStatus(200) })

	h := &ui.Handler{
		DB:      gdb,
		Session: ui.NewSession("secret", []string{"test-key"}),
	}
	ui.Mount(app, h)

	req := httptest.NewRequest(http.MethodGet, "/ui/login", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login should be public, status=%d body=%s", resp.StatusCode, body)
	}
}
