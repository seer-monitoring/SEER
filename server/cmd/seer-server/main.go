package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/seer-monitoring/SEER/server/internal/alerts"
	"github.com/seer-monitoring/SEER/server/internal/api"
	"github.com/seer-monitoring/SEER/server/internal/auth"
	"github.com/seer-monitoring/SEER/server/internal/config"
	"github.com/seer-monitoring/SEER/server/internal/db"
	"github.com/seer-monitoring/SEER/server/internal/ui"
)

func main() {
	cfg := config.Load()
	gdb, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	notifier := alerts.New(gdb, cfg)
	srv := &api.Server{DB: gdb, Notifier: notifier, Cfg: cfg}

	app := fiber.New(fiber.Config{
		DisableStartupMessage: false,
		AppName:               "seer-server-ce",
	})
	app.Use(recover.New())

	app.Get("/health", srv.Health)
	app.Get("/", func(c *fiber.Ctx) error {
		if cfg.UIEnabled {
			return c.Redirect("/ui/", fiber.StatusFound)
		}
		return c.JSON(fiber.Map{"status": "ok", "edition": "community"})
	})

	// Apply API-key auth only to ingest routes — not Group("/"), which would
	// also lock the embedded UI behind Authorization headers.
	authMW := auth.Middleware(cfg.APIKeys)
	app.Post("/monitoring", authMW, srv.Monitoring)
	app.Post("/heartbeat", authMW, srv.Heartbeat)
	app.Get("/check_heartbeat", authMW, srv.CheckHeartbeat)

	ent := app.Group("/enterprise", authMW)
	ent.All("/:feature", srv.EnterpriseStub)
	ent.All("/:feature/*", srv.EnterpriseStub)

	if cfg.UIEnabled {
		uiHandler := &ui.Handler{
			DB:      gdb,
			Session: ui.NewSession(cfg.UISecret, cfg.APIKeys),
			CheckHeartbeats: func() (int, error) {
				return srv.ScanStaleHeartbeats()
			},
		}
		ui.Mount(app, uiHandler)
		log.Printf("UI enabled at /ui (login with SEER_API_KEYS)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if cfg.HeartbeatCheckIntervalSec > 0 {
		interval := time.Duration(cfg.HeartbeatCheckIntervalSec) * time.Second
		go runHeartbeatTicker(ctx, srv, interval)
		log.Printf("heartbeat check ticker every %s", interval)
	}

	go func() {
		log.Printf("seer-server CE listening on %s (db=%s)", cfg.HTTPAddr, cfg.DBPath)
		if cfg.WebhookURL == "" && (cfg.SMTPHost == "" || cfg.SMTPTo == "") {
			log.Printf("alerts: no SEER_WEBHOOK_URL or SEER_SMTP_* set — add channels in /ui/channels or set env to receive notifications")
		}
		if err := app.Listen(cfg.HTTPAddr); err != nil {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancel()
	_ = app.Shutdown()
}

func runHeartbeatTicker(ctx context.Context, srv *api.Server, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := srv.ScanStaleHeartbeats()
			if err != nil {
				log.Printf("heartbeat check failed: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("heartbeat check alerted %d job(s)", n)
			}
		}
	}
}
