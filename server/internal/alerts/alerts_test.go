package alerts_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seer-monitoring/SEER/server/internal/alerts"
	"github.com/seer-monitoring/SEER/server/internal/config"
	"github.com/seer-monitoring/SEER/server/internal/db"
	"github.com/seer-monitoring/SEER/server/internal/models"
)

func TestWebhookNotifyOnFailure(t *testing.T) {
	var hits atomic.Int32
	var body []byte
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		ua = r.Header.Get("User-Agent")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "t.db")
	gdb, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(gdb) })

	n := alerts.New(gdb, config.Config{
		WebhookURL: srv.URL,
	})
	job := models.Job{Name: "j", NotifyOnFailure: true}
	run := models.Run{RunID: "r1", Status: "failed", ErrorDetails: "boom"}
	n.Notify(job, "failed", &run)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && hits.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d", hits.Load())
	}
	if !strings.Contains(ua, "SEER/") {
		t.Fatalf("User-Agent=%q", ua)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != "job.failed" || payload["job_name"] != "j" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestNotifySkippedWithoutDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	gdb, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(gdb) })
	n := alerts.New(gdb, config.Config{})
	// Should not panic; nothing configured.
	n.Notify(models.Job{Name: "j", NotifyOnFailure: true}, "failed", &models.Run{RunID: "x"})
}
