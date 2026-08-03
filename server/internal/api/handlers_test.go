package api_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/seer-monitoring/SEER/server/internal/api"
	"github.com/seer-monitoring/SEER/server/internal/auth"
	"github.com/seer-monitoring/SEER/server/internal/db"
)

func setupApp(t *testing.T) *fiber.App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	gdb, err := db.Open(path)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close(gdb)
	})
	srv := &api.Server{DB: gdb}
	app := fiber.New()
	app.Get("/health", srv.Health)
	g := app.Group("/", auth.Middleware([]string{"test-key"}))
	g.Post("/monitoring", srv.Monitoring)
	g.Post("/heartbeat", srv.Heartbeat)
	ent := app.Group("/enterprise", auth.Middleware([]string{"test-key"}))
	ent.All("/:feature", srv.EnterpriseStub)
	return app
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
	app := setupApp(t)

	body := `{"job_name":"job1","status":"running","run_id":"","start_time":"2026-01-01T00:00:00Z"}`
	req := httptest.NewRequest("POST", "/monitoring", strings.NewReader(body))
	req.Header.Set("Authorization", "test-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "k1:register")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var start map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&start); err != nil {
		t.Fatal(err)
	}
	runID, _ := start["run_id"].(string)
	if runID == "" {
		t.Fatal("expected run_id")
	}

	complete := `{"job_name":"job1","status":"success","run_id":"` + runID + `","start_time":"2026-01-01T00:00:00Z","end_time":"2026-01-01T00:01:00Z"}`
	req2 := httptest.NewRequest("POST", "/monitoring", strings.NewReader(complete))
	req2.Header.Set("Authorization", "test-key")
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "k1:complete")
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != 200 {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("status=%d body=%s", resp2.StatusCode, b)
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
	req := httptest.NewRequest("POST", "/heartbeat", strings.NewReader(`{"job_name":"worker","metadata":{"pid":1}}`))
	req.Header.Set("Authorization", "test-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}
