package alerts

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

	"github.com/seer-monitoring/SEER/server/internal/config"
	"github.com/seer-monitoring/SEER/server/internal/db"
	"github.com/seer-monitoring/SEER/server/internal/models"
)

func TestMarshalWebhookBodyDiscordUsesContent(t *testing.T) {
	payload := webhookPayload{
		Event:   "job.failed",
		JobName: "j",
		Status:  "failed",
		Text:    "Pipeline: j\nStatus: failed",
		Subject: "Job j had a failed event.",
	}
	body, err := marshalWebhookBody("https://discord.com/api/webhooks/1/abc", payload)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["content"] != payload.Text {
		t.Fatalf("content=%q", m["content"])
	}
	if _, ok := m["event"]; ok {
		t.Fatalf("discord body should not include event: %v", m)
	}

	generic, err := marshalWebhookBody("https://hooks.example/seer", payload)
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]any
	if err := json.Unmarshal(generic, &g); err != nil {
		t.Fatal(err)
	}
	if g["event"] != "job.failed" {
		t.Fatalf("generic=%v", g)
	}
}

func TestDiscordShapedChannelNotifySendsContent(t *testing.T) {
	var hits atomic.Int32
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "t.db")
	gdb, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(gdb) })

	// isDiscordWebhook matches on substring; append a Discord-shaped ref so the
	// test server receives a Discord-formatted body without leaving localhost.
	hook := srv.URL + "/hook?via=discord.com/api/webhooks/1/token"
	if !isDiscordWebhook(hook) {
		t.Fatalf("expected discord detection for %q", hook)
	}
	raw, _ := json.Marshal(map[string]string{"url": hook})
	if err := gdb.Create(&models.AlertChannel{
		Type:       "webhook",
		ConfigJSON: string(raw),
		Enabled:    true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	New(gdb, config.Config{}).Notify(
		models.Job{Name: "j", NotifyOnFailure: true},
		"failed",
		&models.Run{RunID: "r1", ErrorDetails: "boom"},
	)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && hits.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d", hits.Load())
	}
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["content"] == "" || !strings.Contains(m["content"], "j") {
		t.Fatalf("content=%q", m["content"])
	}
}
