(() => {
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

  function flash(msg, kind = "ok") {
    const el = $("#flash");
    if (!el) return;
    el.hidden = !msg;
    el.textContent = msg || "";
    el.classList.toggle("error", kind === "error");
    el.classList.toggle("ok", kind === "ok");
  }

  async function api(path, opts = {}) {
    const res = await fetch(path, {
      headers: { Accept: "application/json", ...(opts.body ? { "Content-Type": "application/json" } : {}), ...(opts.headers || {}) },
      credentials: "same-origin",
      ...opts,
    });
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch { data = { error: text }; }
    if (!res.ok) {
      const err = (data && data.error) || res.statusText || "request failed";
      throw new Error(err);
    }
    return data;
  }

  function esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function fmtTime(v) {
    if (!v) return "—";
    try { return new Date(v).toLocaleString(); } catch { return String(v); }
  }

  function statusBadge(status) {
    const s = (status || "unknown").toLowerCase();
    return `<span class="badge ${esc(s)}">${esc(s)}</span>`;
  }

  async function loadJobs() {
    const root = $("#jobs-root");
    if (!root) return;
    try {
      const data = await api("/api/ui/jobs");
      const jobs = data.jobs || [];
      if (!jobs.length) {
        root.innerHTML = `<div class="empty">${esc(root.dataset.empty || "No jobs yet.")}</div>`;
        return;
      }
      root.innerHTML = `<table class="data"><thead><tr>
        <th>Job</th><th>Last status</th><th>Last run</th><th>Heartbeat</th><th></th>
      </tr></thead><tbody>
      ${jobs.map((j) => `<tr>
        <td><a href="/ui/jobs/${encodeURIComponent(j.name)}">${esc(j.name)}</a></td>
        <td>${j.last_status ? statusBadge(j.last_status) : '<span class="muted">—</span>'}</td>
        <td>${esc(fmtTime(j.last_run_at))}</td>
        <td>${j.stale ? '<span class="badge stale">stale</span>' : (j.heartbeat_at ? `<span class="badge ok-hb">${esc(fmtTime(j.heartbeat_at))}</span>` : '<span class="muted">—</span>')}</td>
        <td><a href="/ui/jobs/${encodeURIComponent(j.name)}">Open</a></td>
      </tr>`).join("")}
      </tbody></table>`;
    } catch (e) {
      root.innerHTML = `<p class="flash error">${esc(e.message)}</p>`;
    }
  }

  async function loadJob(name) {
    const data = await api(`/api/ui/jobs/${encodeURIComponent(name)}`);
    const job = data.job;
    const form = $("#job-settings");
    if (form) {
      form.notify_on_start.checked = !!job.notify_on_start;
      form.notify_on_success.checked = !!job.notify_on_success;
      form.notify_on_failure.checked = !!job.notify_on_failure;
      form.notify_on_heartbeat_missed.checked = !!job.notify_on_heartbeat_missed;
      form.heartbeat_stale_after_sec.value = job.heartbeat_stale_after_sec || 300;
    }
    const hb = $("#heartbeat-panel");
    if (hb) {
      if (!data.heartbeat) {
        hb.innerHTML = `<h2>Heartbeat</h2><p class="muted">No heartbeat recorded yet.</p>`;
      } else {
        hb.innerHTML = `<h2>Heartbeat</h2>
          <dl class="kv">
            <dt>Last seen</dt><dd>${esc(fmtTime(data.heartbeat.seen_at))}</dd>
            <dt>Status</dt><dd>${job.stale ? '<span class="badge stale">stale</span>' : '<span class="badge ok-hb">fresh</span>'}</dd>
            <dt>Stale after</dt><dd>${esc(job.heartbeat_stale_after_sec)}s</dd>
          </dl>`;
      }
    }
    const runsRoot = $("#runs-root");
    if (runsRoot) {
      const runs = data.runs || [];
      if (!runs.length) {
        runsRoot.innerHTML = `<div class="empty">No runs yet.</div>`;
      } else {
        runsRoot.innerHTML = `<table class="data"><thead><tr>
          <th>Status</th><th>Run ID</th><th>Started</th><th>Ended</th><th>Error</th>
        </tr></thead><tbody>
        ${runs.map((r) => `<tr>
          <td>${statusBadge(r.status)}</td>
          <td><a class="mono" href="/ui/runs/${encodeURIComponent(r.run_id)}">${esc(r.run_id.slice(0, 8))}…</a></td>
          <td>${esc(fmtTime(r.start_time))}</td>
          <td>${esc(fmtTime(r.end_time))}</td>
          <td class="muted">${esc(r.error_details || "—")}</td>
        </tr>`).join("")}
        </tbody></table>`;
      }
    }
  }

  async function loadRun(runId) {
    const root = $("#run-root");
    if (!root) return;
    try {
      const data = await api(`/api/ui/runs/${encodeURIComponent(runId)}`);
      const run = data.run;
      const jobLink = $("#run-job-link");
      if (jobLink) {
        jobLink.href = `/ui/jobs/${encodeURIComponent(data.job_name)}`;
        jobLink.textContent = data.job_name;
      }
      root.innerHTML = `
        <dl class="kv">
          <dt>Job</dt><dd><a href="/ui/jobs/${encodeURIComponent(data.job_name)}">${esc(data.job_name)}</a></dd>
          <dt>Status</dt><dd>${statusBadge(run.status)}</dd>
          <dt>Start</dt><dd>${esc(fmtTime(run.start_time))}</dd>
          <dt>End</dt><dd>${esc(fmtTime(run.end_time))}</dd>
          <dt>Tags</dt><dd><pre class="mono" style="margin:0">${esc(JSON.stringify(data.tags ?? null, null, 2))}</pre></dd>
          <dt>Metadata</dt><dd><pre class="mono" style="margin:0">${esc(JSON.stringify(data.metadata ?? null, null, 2))}</pre></dd>
          <dt>Error</dt><dd>${esc(run.error_details || "—")}</dd>
        </dl>
        <h2>Logs</h2>
        <pre class="logs">${esc(run.logs || "(empty)")}</pre>`;
    } catch (e) {
      root.innerHTML = `<p class="flash error">${esc(e.message)}</p>`;
    }
  }

  async function loadChannels() {
    const root = $("#channels-root");
    if (!root) return;
    try {
      const data = await api("/api/ui/channels");
      const channels = data.channels || [];
      if (!channels.length) {
        root.innerHTML = `<div class="empty">No channels yet. Add a Slack webhook or email recipient above.</div>`;
        return;
      }
      root.innerHTML = `<table class="data"><thead><tr>
        <th>ID</th><th>Type</th><th>Config</th><th>Enabled</th><th></th>
      </tr></thead><tbody>
      ${channels.map((ch) => `<tr>
        <td>${esc(ch.id)}</td>
        <td>${esc(ch.type)}</td>
        <td class="mono">${esc(typeof ch.config === "string" ? ch.config : JSON.stringify(ch.config || {}))}</td>
        <td>
          <button type="button" class="btn ghost" data-toggle-channel="${esc(ch.id)}" data-enabled="${ch.enabled ? "1" : "0"}">
            ${ch.enabled ? "On" : "Off"}
          </button>
        </td>
        <td><button type="button" class="btn danger" data-del-channel="${esc(ch.id)}">Delete</button></td>
      </tr>`).join("")}
      </tbody></table>`;
    } catch (e) {
      root.innerHTML = `<p class="flash error">${esc(e.message)}</p>`;
    }
  }

  function page() {
    const path = location.pathname.replace(/\/$/, "") || "/ui";
    if (path === "/ui" || path === "/ui/") return "jobs";
    if (path.startsWith("/ui/jobs/")) return "job";
    if (path.startsWith("/ui/runs/")) return "run";
    if (path.startsWith("/ui/channels")) return "channels";
    return "";
  }

  document.addEventListener("DOMContentLoaded", () => {
    $$("[data-refresh]").forEach((btn) => {
      btn.addEventListener("click", () => location.reload());
    });

    const checkHb = $("#check-hb");
    if (checkHb) {
      checkHb.addEventListener("click", async () => {
        try {
          const data = await api("/api/ui/check_heartbeat", { method: "POST" });
          flash(`Heartbeat check done. Alerted ${data.alerted || 0} job(s).`);
          await loadJobs();
        } catch (e) {
          flash(e.message, "error");
        }
      });
    }

    const p = page();
    if (p === "jobs") loadJobs();
    if (p === "job") {
      const name = decodeURIComponent(location.pathname.split("/").pop());
      loadJob(name).catch((e) => flash(e.message, "error"));
      const form = $("#job-settings");
      if (form) {
        form.addEventListener("submit", async (ev) => {
          ev.preventDefault();
          try {
            await api(`/api/ui/jobs/${encodeURIComponent(name)}`, {
              method: "PATCH",
              body: JSON.stringify({
                notify_on_start: form.notify_on_start.checked,
                notify_on_success: form.notify_on_success.checked,
                notify_on_failure: form.notify_on_failure.checked,
                notify_on_heartbeat_missed: form.notify_on_heartbeat_missed.checked,
                heartbeat_stale_after_sec: Number(form.heartbeat_stale_after_sec.value),
              }),
            });
            flash("Settings saved.");
            await loadJob(name);
          } catch (e) {
            flash(e.message, "error");
          }
        });
      }
    }
    if (p === "run") {
      const runId = decodeURIComponent(location.pathname.split("/").pop());
      loadRun(runId);
    }
    if (p === "channels") {
      loadChannels();
      const typeSel = $('select[name="type"]');
      const slackLabel = $("#cfg-slack-label");
      const emailLabel = $("#cfg-email-label");
      const syncType = () => {
        const slack = typeSel && typeSel.value === "slack";
        if (slackLabel) slackLabel.hidden = !slack;
        if (emailLabel) emailLabel.hidden = slack;
      };
      if (typeSel) typeSel.addEventListener("change", syncType);
      syncType();
      const form = $("#channel-form");
      if (form) {
        form.addEventListener("submit", async (ev) => {
          ev.preventDefault();
          const type = form.type.value;
          const config = type === "slack"
            ? { webhook_url: form.webhook_url.value.trim() }
            : { to: form.to.value.trim() };
          try {
            await api("/api/ui/channels", {
              method: "POST",
              body: JSON.stringify({ type, enabled: form.enabled.checked, config }),
            });
            flash("Channel added.");
            form.reset();
            form.enabled.checked = true;
            syncType();
            await loadChannels();
          } catch (e) {
            flash(e.message, "error");
          }
        });
      }
      document.addEventListener("click", async (ev) => {
        const t = ev.target.closest("[data-toggle-channel]");
        const d = ev.target.closest("[data-del-channel]");
        try {
          if (t) {
            const id = t.getAttribute("data-toggle-channel");
            const enabled = t.getAttribute("data-enabled") !== "1";
            await api(`/api/ui/channels/${id}`, { method: "PATCH", body: JSON.stringify({ enabled }) });
            flash(enabled ? "Channel enabled." : "Channel disabled.");
            await loadChannels();
          }
          if (d) {
            const id = d.getAttribute("data-del-channel");
            if (!confirm("Delete this channel?")) return;
            await api(`/api/ui/channels/${id}`, { method: "DELETE" });
            flash("Channel deleted.");
            await loadChannels();
          }
        } catch (e) {
          flash(e.message, "error");
        }
      });
    }
  });
})();
