package ui

import (
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"

	"github.com/seer-monitoring/SEER/server/web"
)

type pageData struct {
	Title   string
	Nav     string
	Authed  bool
	Panel   bool
	Error   string
	Next    string
	JobName string
	RunID   string
}

func (h *Handler) templates(page string) (*template.Template, error) {
	return template.New("layout.html").ParseFS(web.FS, "templates/layout.html", "templates/"+page+".html")
}

func (h *Handler) render(c *fiber.Ctx, page string, data pageData) error {
	tmpl, err := h.templates(page)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	if err := tmpl.ExecuteTemplate(&b, "layout.html", data); err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}
	return c.SendString(b.String())
}

func (h *Handler) PageLogin(c *fiber.Ctx) error {
	if h.Session.Authenticated(c) {
		return c.Redirect("/ui/", fiber.StatusFound)
	}
	next := c.Query("next", "/ui/")
	return h.render(c, "login", pageData{
		Title:  "Login",
		Authed: false,
		Panel:  true,
		Next:   next,
		Error:  c.Query("error"),
	})
}

func (h *Handler) PostLogin(c *fiber.Ctx) error {
	apiKey := strings.TrimSpace(c.FormValue("api_key"))
	next := c.FormValue("next")
	if next == "" {
		next = "/ui/"
	}
	if !h.Session.ValidAPIKey(apiKey) {
		return c.Redirect("/ui/login?error="+url.QueryEscape("Invalid API key")+"&next="+url.QueryEscape(next), fiber.StatusFound)
	}
	h.Session.SetCookie(c, apiKey)
	if !strings.HasPrefix(next, "/ui") {
		next = "/ui/"
	}
	return c.Redirect(next, fiber.StatusFound)
}

func (h *Handler) PostLogout(c *fiber.Ctx) error {
	h.Session.ClearCookie(c)
	return c.Redirect("/ui/login", fiber.StatusFound)
}

func (h *Handler) PageJobs(c *fiber.Ctx) error {
	return h.render(c, "jobs", pageData{Title: "Jobs", Nav: "jobs", Authed: true, Panel: true})
}

func (h *Handler) PageJob(c *fiber.Ctx) error {
	name := c.Params("name")
	return h.render(c, "job", pageData{Title: name, Nav: "jobs", Authed: true, Panel: true, JobName: name})
}

func (h *Handler) PageRun(c *fiber.Ctx) error {
	runID := c.Params("run_id")
	return h.render(c, "run", pageData{Title: "Run", Nav: "jobs", Authed: true, Panel: true, RunID: runID})
}

func (h *Handler) PageChannels(c *fiber.Ctx) error {
	return h.render(c, "channels", pageData{Title: "Channels", Nav: "channels", Authed: true, Panel: true})
}

// Mount registers UI pages, static assets, and /api/ui routes on app.
func Mount(app *fiber.App, h *Handler) {
	static, err := fs.Sub(web.FS, "static")
	if err == nil {
		app.Use("/ui/static", filesystem.New(filesystem.Config{
			Root:   http.FS(static),
			Browse: false,
		}))
	}

	app.Get("/ui/login", h.PageLogin)
	app.Post("/ui/login", h.PostLogin)
	app.Post("/ui/logout", h.PostLogout)

	ui := app.Group("/ui", h.Session.RequireAuth())
	ui.Get("/", h.PageJobs)
	ui.Get("/jobs/:name", h.PageJob)
	ui.Get("/runs/:run_id", h.PageRun)
	ui.Get("/channels", h.PageChannels)

	api := app.Group("/api/ui", h.Session.RequireAuth())
	api.Get("/jobs", h.ListJobs)
	api.Get("/jobs/:name", h.GetJob)
	api.Patch("/jobs/:name", h.PatchJob)
	api.Get("/runs/:run_id", h.GetRun)
	api.Get("/channels", h.ListChannels)
	api.Post("/channels", h.CreateChannel)
	api.Patch("/channels/:id", h.PatchChannel)
	api.Delete("/channels/:id", h.DeleteChannel)
	api.Post("/check_heartbeat", h.CheckHeartbeat)
}
