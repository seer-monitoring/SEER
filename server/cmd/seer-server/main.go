package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/seer-monitoring/SEER/server/internal/alerts"
	"github.com/seer-monitoring/SEER/server/internal/api"
	"github.com/seer-monitoring/SEER/server/internal/auth"
	"github.com/seer-monitoring/SEER/server/internal/config"
	"github.com/seer-monitoring/SEER/server/internal/db"
)

func main() {
	cfg := config.Load()
	gdb, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	notifier := alerts.New(gdb, cfg)
	srv := &api.Server{DB: gdb, Notifier: notifier}

	app := fiber.New(fiber.Config{
		DisableStartupMessage: false,
		AppName:               "seer-server-ce",
	})
	app.Use(recover.New())

	app.Get("/health", srv.Health)

	apiGroup := app.Group("/", auth.Middleware(cfg.APIKeys))
	apiGroup.Post("/monitoring", srv.Monitoring)
	apiGroup.Post("/heartbeat", srv.Heartbeat)

	ent := app.Group("/enterprise", auth.Middleware(cfg.APIKeys))
	ent.All("/:feature", srv.EnterpriseStub)
	ent.All("/:feature/*", srv.EnterpriseStub)

	go func() {
		log.Printf("seer-server CE listening on %s (db=%s)", cfg.HTTPAddr, cfg.DBPath)
		if err := app.Listen(cfg.HTTPAddr); err != nil {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	_ = app.Shutdown()
}
