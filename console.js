(function () {
  const API = "";
  function go(p) {
    document.querySelectorAll(".page").forEach(s => s.classList.toggle("on", s.id === "p-" + p));
    document.querySelectorAll(".nav[data-p]").forEach(n => n.classList.toggle("on", n.dataset.p === p));
  }
  window.go = go;

  async function j(url, opts) {
    const r = await fetch(API + url, opts);
    if (!r.ok) throw new Error(await r.text());
    return r.json();
  }

  function fmtGB(bytes) {
    if (!bytes) return "—";
    return (bytes / (1024 ** 3)).toFixed(1) + " GB";
  }

  async function refreshTopBar() {
    try {
      const [eng, cfg] = await Promise.all([j("/engine/status"), j("/config/settings")]);
      const pill = document.querySelector(".model-pill");
      if (!pill) return;
      const nameEl = pill.querySelector(".model-name");
      const metas = pill.querySelectorAll(".meta");
      const path = eng.model_path || "";
      const base = path.split(/[/\\]/).pop() || path;
      if (nameEl) nameEl.textContent = base ? base.replace(/\.gguf$/i, "") : "No model loaded";
      if (metas[0]) metas[0].textContent = eng.backend ? eng.backend.toUpperCase() : "CPU";
      const used = eng.vram_used_mb || 0;
      const ceil = eng.vram_ceil_mb || 0;
      const bar = pill.querySelector(".bar i");
      if (bar && ceil > 0) bar.style.width = Math.min(100, (used / ceil) * 100) + "%";
      const vramMeta = pill.querySelector(".vram .meta");
      if (vramMeta) vramMeta.textContent = "VRAM " + (used / 1024).toFixed(1) + " / " + (ceil / 1024).toFixed(1) + " GB";
      setText("#stat-loaded", base ? base.replace(/\.gguf$/i, "") : "None");
      setText("#stat-vram", ceil ? (used / 1024).toFixed(1) + " / " + (ceil / 1024).toFixed(1) + " GB" : "—");
      const endpoint = document.querySelector(".top-right .endpoint");
      if (endpoint && cfg.api_base) endpoint.textContent = cfg.api_base.replace(/^https?:\/\//, "");
    } catch (_) {}
  }

  // Same rule Discover uses (pkg/hfhub Fit): room for the file plus 1.5 GB = yes; up to twice the memory = partly.
  function fitTag(bytes, vramMB) {
    if (!bytes || !vramMB) return '<span class="note">—</span>';
    const vram = vramMB * 1024 * 1024;
    const fit = bytes + 1.5 * 1024 ** 3 <= vram ? "yes" : bytes <= 2 * vram ? "partly" : "too_big";
    const [cls, label] = FIT[fit];
    return '<span class="tag ' + cls + '"><i></i>' + label + "</span>";
  }

  async function refreshModels() {
    const tbody = document.querySelector("#p-models tbody");
    if (!tbody) return;
    try {
      const [data, eng] = await Promise.all([j("/models/list"), j("/engine/status")]);
      const models = data.models || [];
      // The engine reports C:/ paths and the list gives C:\ paths; compare them the same way.
      const samePath = p => String(p || "").replace(/\\/g, "/").toLowerCase();
      const loadedPath = samePath(eng.model_path);
      tbody.innerHTML = "";
      if (!models.length) {
        tbody.innerHTML = '<tr><td colspan="4" class="note">No models found. Add a .gguf folder in Settings, or use Discover.</td></tr>';
      }
      for (const m of models) {
        const tr = document.createElement("tr");
        const size = m.size_bytes ? fmtGB(m.size_bytes) : "—";
        const isLoaded = loadedPath && m.path && loadedPath === samePath(m.path);
        const loadBtn = isLoaded
          ? '<button class="btn sm" data-unload>Unload</button>'
          : '<button class="btn sm primary" data-load="' + escapeAttr(m.path || m.name) + '">Load</button>';
        tr.innerHTML =
          '<td><div class="name">' + escapeHtml(m.name) +
          (isLoaded ? ' <span class="tag live" style="margin-left:6px"><i style="background:var(--ok)"></i>Loaded</span>' : "") +
          (m.source === "ollama" ? ' <span class="tag" style="margin-left:6px">Ollama</span>' : "") +
          (m.kind === "embedding" ? ' <span class="tag warn" style="margin-left:6px" title="Turns text into numbers for search tools. It can\'t chat."><i></i>Embedding, not for chat</span>' : "") +
          '</div>' +
          (m.aliases && m.aliases.length ? '<div class="note">Same file as: ' + escapeHtml(m.aliases.join(", ")) + '</div>' : "") +
          '<div class="path">' + escapeHtml(m.path || "") + '</div></td>' +
          '<td class="mono hide-sm">' + size + '</td>' +
          '<td style="white-space:nowrap">' + fitTag(m.size_bytes, eng.vram_ceil_mb) + '</td>' +
          '<td><div class="row-actions">' + loadBtn + '</div></td>';
        tbody.appendChild(tr);
      }
      tbody.querySelectorAll("[data-load]").forEach(btn => {
        btn.addEventListener("click", async () => {
          btn.disabled = true;
          btn.textContent = "Loading…";
          try {
            await j("/models/load", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ model_path: btn.getAttribute("data-load") }) });
            await refreshTopBar();
            await refreshModels();
          } catch (e) { alert(e.message); btn.textContent = "Load"; }
          btn.disabled = false;
        });
      });
      tbody.querySelectorAll("[data-unload]").forEach(btn => {
        btn.addEventListener("click", async () => {
          btn.disabled = true;
          try {
            await j("/models/unload", { method: "POST" });
            await refreshTopBar();
            await refreshModels();
          } catch (e) { alert(e.message); }
          btn.disabled = false;
        });
      });
      const count = document.querySelector('.nav[data-p="models"] .count');
      if (count) count.textContent = String(models.length);
      const title = document.querySelector("#p-models .panel-head .title");
      if (title) title.textContent = models.length + " models";
      setText("#stat-found", String(models.length));
    } catch (e) {
      tbody.innerHTML = '<tr><td colspan="4" class="note">' + escapeHtml(e.message) + '</td></tr>';
    }
  }

  async function refreshRequestLog() {
    const tbody = document.querySelector("#p-server .log tbody");
    if (!tbody) return;
    try {
      const data = await j("/config/request-log");
      const rows = data.entries || [];
      setText("#stat-requests", String(rows.length));
      tbody.innerHTML = "";
      if (!rows.length) {
        tbody.innerHTML = '<tr><td colspan="6" class="note">No requests yet. Point an app at the Server address.</td></tr>';
        return;
      }
      for (const e of rows.slice(0, 50)) {
        const tr = document.createElement("tr");
        const ok = e.status < 400;
        tr.innerHTML =
          "<td>" + escapeHtml(e.time) + "</td><td class=\"c\">" + escapeHtml(e.client || "—") + "</td><td>" + escapeHtml(e.model || "—") + "</td>" +
          '<td class="hide-sm">' + escapeHtml(e.tokens || "—") + "</td><td>" + escapeHtml(e.speed || "—") + "</td>" +
          '<td><span class="tag ' + (ok ? "ok" : "bad") + '"><i></i>' + escapeHtml(String(e.status)) + "</span></td>";
        tbody.appendChild(tr);
      }
    } catch (_) {}
  }

  // "Connect your app" examples, one per tab, built from the real address.
  let snipBase = location.origin + "/v1";
  let snipTab = "python";
  const SNIPPETS = {
    python: b => ({
      note: "Install once: uv pip install openai",
      code: 'from openai import OpenAI\n' +
        'client = OpenAI(base_url="' + b + '", api_key="janus")\n' +
        'reply = client.chat.completions.create(\n' +
        '    model="local",\n' +
        '    messages=[{"role": "user", "content": "Hello"}])\n' +
        'print(reply.choices[0].message.content)'
    }),
    curl: b => ({
      note: "Works in Command Prompt, PowerShell 7 and Linux terminals.",
      code: 'curl ' + b + '/chat/completions -H "Content-Type: application/json" ' +
        '-d "{\\"model\\":\\"local\\",\\"messages\\":[{\\"role\\":\\"user\\",\\"content\\":\\"Hello\\"}]}"'
    }),
    vscode: b => ({
      note: "Continue extension: open its config.yaml and add this under models.",
      code: 'models:\n' +
        '  - name: Janus\n' +
        '    provider: openai\n' +
        '    model: local\n' +
        '    apiBase: ' + b + '\n' +
        '    apiKey: janus'
    }),
    openwebui: b => ({
      note: "Open WebUI: Settings > Connections > OpenAI API > add a connection. If Open WebUI runs in Docker, use the second address.",
      code: 'URL:  ' + b + '\n' +
        'Docker URL:  ' + b.replace(/\/\/(127\.0\.0\.1|localhost)/, "//host.docker.internal") + '\n' +
        'API key:  janus\n' +
        'Model:  local'
    }),
    vibe: b => ({
      note: "A Vibe Engine recipe step that asks the model loaded in Janus.",
      code: JSON.stringify({
        id: "ask_janus",
        action_type: "http_request",
        target: b + "/chat/completions",
        payload: {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: { model: "local", messages: [{ role: "user", content: "Hello" }] }
        },
        output: { store_as: "janus" }
      }, null, 2) + "\n\nThe reply is in {{janus.choices[0].message.content}}"
    })
  };

  function renderSnippet() {
    const code = document.querySelector("#snip-code");
    if (!code) return;
    const s = (SNIPPETS[snipTab] || SNIPPETS.python)(snipBase);
    code.textContent = s.code;
    setText("#snip-note", s.note);
  }

  async function refreshServerPanel() {
    try {
      const cfg = await j("/config/settings");
      const addrEl = document.querySelector("#p-server .field .code");
      if (addrEl && cfg.api_base) addrEl.textContent = cfg.api_base;
      if (cfg.api_base) snipBase = cfg.api_base;
      renderSnippet();
      const portInput = document.querySelector("#set-port");
      if (portInput && cfg.listen) {
        const m = cfg.listen.match(/:(\d+)$/);
        if (m) portInput.value = m[1];
      }
    } catch (_) {}
  }

  async function refreshPeers() {
    const host = document.querySelector("#tools-list");
    if (!host) return;
    try {
      const data = await j("/config/peers");
      const peers = data.peers || [];
      const srvCount0 = document.querySelector('.nav[data-p="server"] .count');
      if (srvCount0) srvCount0.textContent = "";
      if (!peers.length) {
        host.innerHTML = '<div class="note" style="padding:10px">No other tools running. Start Terminus, Vox Vena, or Vibe Engine and they appear here.</div>';
        return;
      }
      host.innerHTML = "";
      for (const p of peers) {
        const row = document.createElement("div");
        row.className = "nav";
        row.style.cursor = "default";
        row.innerHTML = '<span style="flex:1">' + escapeHtml(p.name || p.id) + '</span><span class="count">' + (p.ok ? (p.version || "ok") : "offline") + "</span>";
        host.appendChild(row);
      }
      const srvCount = document.querySelector('.nav[data-p="server"] .count');
      if (srvCount) srvCount.textContent = peers.filter(p => p.ok).length + " apps";
    } catch (_) {}
  }

  // ---- Discover: Hugging Face search and downloads ----
  const FIT = { yes: ["ok", "Yes"], partly: ["warn", "Partly"], too_big: ["bad", "Too big"] };
  let hubRepo = "";

  function fmtCount(n) {
    if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
    if (n >= 1e3) return Math.round(n / 1e3) + "k";
    return String(n || 0);
  }
  function fmtAgo(iso) {
    const t = Date.parse(iso);
    if (!t) return "";
    const days = Math.floor((Date.now() - t) / 86400000);
    if (days < 1) return "today";
    if (days < 30) return days + (days === 1 ? " day ago" : " days ago");
    const months = Math.floor(days / 30);
    if (months < 12) return months + (months === 1 ? " month ago" : " months ago");
    return Math.floor(months / 12) + " yr ago";
  }

  async function hubSearch() {
    const q = document.querySelector("#hub-q").value.trim();
    const sort = document.querySelector("#hub-sort").value;
    const body = document.querySelector("#hub-repos");
    const count = document.querySelector("#hub-count");
    body.innerHTML = '<tr><td class="note">Searching Hugging Face…</td></tr>';
    count.textContent = "";
    try {
      const data = await j("/models/search?q=" + encodeURIComponent(q) + "&sort=" + encodeURIComponent(sort));
      const repos = data.repos || [];
      count.textContent = repos.length + (repos.length === 1 ? " repository" : " repositories");
      body.innerHTML = repos.length ? "" : '<tr><td class="note">No GGUF models found for that search.</td></tr>';
      for (const r of repos) {
        const tr = document.createElement("tr");
        tr.style.cursor = "pointer";
        tr.innerHTML = '<td><div class="name">' + escapeHtml(r.id) + '</div><div class="path">' +
          fmtCount(r.downloads) + " downloads · updated " + escapeHtml(fmtAgo(r.last_modified)) + "</div></td>";
        tr.addEventListener("click", () => {
          body.querySelectorAll("tr").forEach(x => x.style.background = "");
          tr.style.background = "rgba(113,112,255,0.07)";
          hubFiles(r.id);
        });
        body.appendChild(tr);
      }
    } catch (e) {
      body.innerHTML = '<tr><td class="note">' + escapeHtml(errText(e)) + "</td></tr>";
    }
  }

  async function hubFiles(repo) {
    hubRepo = repo;
    document.querySelector("#hub-repo-title").textContent = repo.split("/").pop();
    const body = document.querySelector("#hub-files");
    body.innerHTML = '<tr><td colspan="4" class="note">Loading files…</td></tr>';
    try {
      const data = await j("/models/files?repo=" + encodeURIComponent(repo));
      if (repo !== hubRepo) return;
      document.querySelector("#hub-vram").textContent = "budget " + fmtGB(data.vram_bytes);
      const files = data.files || [];
      body.innerHTML = files.length ? "" : '<tr><td colspan="4" class="note">No model files in this repository.</td></tr>';
      for (const f of files) {
        const [cls, label] = FIT[f.fit] || ["", f.fit];
        const tr = document.createElement("tr");
        tr.innerHTML = '<td class="mono">' + escapeHtml(f.quant || f.name) +
          (f.best ? ' <span class="tag" style="margin-left:4px">Best for this PC</span>' : "") +
          (f.parts.length > 1 ? ' <span class="note">' + f.parts.length + " parts</span>" : "") +
          '<div class="path">' + escapeHtml(f.name) + "</div></td>" +
          '<td class="mono" style="white-space:nowrap">' + fmtGB(f.size_bytes) + "</td>" +
          '<td style="white-space:nowrap"><span class="tag ' + cls + '"><i></i>' + label + "</span></td>" +
          '<td><button class="btn sm' + (f.best ? " primary" : "") + '">Download</button></td>';
        tr.querySelector("button").addEventListener("click", () => hubDownload(repo, f.name));
        body.appendChild(tr);
      }
    } catch (e) {
      body.innerHTML = '<tr><td colspan="4" class="note">' + escapeHtml(errText(e)) + "</td></tr>";
    }
  }

  async function hubDownload(repo, name) {
    try {
      await j("/models/download", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ repo, name }) });
    } catch (e) { alert(errText(e)); }
    refreshDownloads();
  }

  async function hubAction(id, action) {
    await j("/models/downloads", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ id, action }) });
    refreshDownloads();
  }

  let lastDoneCount = 0;
  async function refreshDownloads() {
    const host = document.querySelector("#hub-downloads");
    if (!host) return;
    try {
      const data = await j("/models/downloads");
      const jobs = (data.downloads || []).filter(d => d.state !== "cancelled");
      if (!jobs.length) { host.innerHTML = '<div class="note">Nothing downloading.</div>'; return; }
      host.innerHTML = "";
      for (const d of jobs) {
        const pct = d.total_bytes ? Math.min(100, (d.done_bytes / d.total_bytes) * 100) : 0;
        const row = document.createElement("div");
        row.style.cssText = "display:flex;align-items:center;gap:12px;margin:6px 0;flex-wrap:wrap";
        let status = fmtGB(d.done_bytes) + " / " + fmtGB(d.total_bytes);
        if (d.state === "downloading" && d.bytes_per_sec) status += " · " + (d.bytes_per_sec / 1e6).toFixed(0) + " MB/s";
        if (d.state === "paused") status += " · paused";
        if (d.state === "done") status = "Done · in My Models";
        if (d.state === "failed") status = "Failed: " + d.error;
        let buttons = "";
        if (d.state === "downloading") buttons = '<button class="btn ghost sm" data-a="pause">Pause</button><button class="btn ghost sm" data-a="cancel">Cancel</button>';
        if (d.state === "paused") buttons = '<button class="btn ghost sm" data-a="resume">Resume</button><button class="btn ghost sm" data-a="cancel">Cancel</button>';
        if (d.state === "failed") buttons = '<button class="btn ghost sm" data-a="resume">Try again</button>';
        row.innerHTML = '<span class="mono" style="color:var(--text)">' + escapeHtml(d.name) + '</span><div class="progress"><i style="width:' + pct.toFixed(1) + '%"></i></div><span class="mono">' + escapeHtml(status) + "</span>" + buttons;
        row.querySelectorAll("[data-a]").forEach(b => b.addEventListener("click", () => {
          const a = b.getAttribute("data-a");
          if (a === "resume") hubDownload(d.repo, d.name); else hubAction(d.id, a);
        }));
        host.appendChild(row);
      }
      const doneNow = jobs.filter(d => d.state === "done").length;
      if (doneNow !== lastDoneCount) { lastDoneCount = doneNow; refreshModels(); }
    } catch (_) {}
  }

  function errText(e) {
    try { return JSON.parse(e.message).error || e.message; } catch (_) { return e.message; }
  }

  let settingsCache = {};
  const CTX_SIZES = [2048, 4096, 8192, 16384, 32768, 65536, 131072];
  const MAX_TOKEN_STEPS = [512, 1024, 2048, 4096, 8192, 16384, 32768, 65536];

  function ctxIndexFromVal(n) {
    n = parseInt(n, 10) || 8192;
    let best = 2;
    for (let i = 0; i < CTX_SIZES.length; i++) {
      if (CTX_SIZES[i] <= n) best = i;
    }
    return best;
  }

  function maxTokIndexFromVal(n) {
    n = parseInt(n, 10) || 4096;
    let best = 4;
    for (let i = 0; i < MAX_TOKEN_STEPS.length; i++) {
      if (MAX_TOKEN_STEPS[i] <= n) best = i;
    }
    return best;
  }

  function fmtCtx(n) {
    return Number(n).toLocaleString("en-US");
  }

  async function refreshModelFolders() {
    const tbody = document.querySelector("#model-folders");
    if (!tbody) return;
    try {
      const data = await j("/config/model-folders");
      const rows = data.folders || [];
      if (!rows.length) {
        tbody.innerHTML = '<tr><td colspan="3" class="note">No folders configured.</td></tr>';
        return;
      }
      tbody.innerHTML = "";
      for (const f of rows) {
        const tr = document.createElement("tr");
        const rm = f.removable
          ? '<button class="btn ghost sm" data-rm="' + escapeAttr(f.path) + '">Remove</button>'
          : '<span class="note">built-in</span>';
        tr.innerHTML =
          '<td class="path">' + escapeHtml(f.path) + "</td>" +
          '<td class="mono">' + f.model_count + "</td>" +
          '<td class="row-actions">' + rm + "</td>";
        tr.querySelector("[data-rm]")?.addEventListener("click", async () => {
          await j("/config/model-folders", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action: "remove", path: f.path })
          });
          refreshModelFolders();
          refreshModels();
        });
        tbody.appendChild(tr);
      }
    } catch (_) {
      tbody.innerHTML = '<tr><td colspan="3" class="note">Could not load folders.</td></tr>';
    }
  }

  async function refreshSettingsForm() {
    try {
      const data = await j("/config/settings");
      settingsCache = data.values || {};
      const verRow = document.querySelector("#settings-version");
      if (verRow) {
        const v = await j("/version");
        verRow.textContent = (v.current || "?") + " · " + (v.build_commit || "?") + " · built " + (v.build_time || "?");
      }
      setText("#settings-path", data.path || "—");
      bindSettingsControls();
      refreshModelFolders();
    } catch (_) {}
  }

  function bindSettingsControls() {
    const v = settingsCache;
    const defer = document.querySelector("#set-defer-load");
    if (defer) {
      const on = v.JANUS_DEFER_MODEL_LOAD === "1" || v.JANUS_DEFER_MODEL_LOAD === "true";
      defer.value = on ? "1" : "0";
    }
    const backend = document.querySelector("#set-backend");
    if (backend) backend.value = (v.INFERENCE_BACKEND || "vulkan").toLowerCase();
    const gpu = document.querySelector("#set-gpu-layers");
    const gpuVal = document.querySelector("#set-gpu-layers-val");
    if (gpu) {
      const n = v.JANUS_GPU_LAYERS !== undefined && v.JANUS_GPU_LAYERS !== "" ? v.JANUS_GPU_LAYERS : "-1";
      gpu.value = n;
      if (gpuVal) gpuVal.textContent = n === "-1" || n === "99" ? "all" : n + " layers";
    }
    const vram = document.querySelector("#set-vram-ceiling");
    const vramVal = document.querySelector("#set-vram-ceiling-val");
    if (vram) {
      const mb = parseInt(v.JANUS_VRAM_CEILING_MB || "12083", 10);
      vram.value = mb;
      if (vramVal) vramVal.textContent = (mb / 1024).toFixed(1) + " GB";
    }
    const ctx = document.querySelector("#set-ctx");
    const ctxVal = document.querySelector("#set-ctx-val");
    if (ctx) {
      ctx.value = ctxIndexFromVal(v.JANUS_CTX_SIZE || "8192");
      if (ctxVal) ctxVal.textContent = fmtCtx(CTX_SIZES[ctx.value] || 8192);
    }
    const pf = document.querySelector("#set-prompt-format");
    if (pf) pf.value = v.JANUS_PROMPT_FORMAT || "";
    const think = document.querySelector("#set-think");
    if (think) think.classList.toggle("on", v.JANUS_THINK !== "false");
    const maxT = document.querySelector("#set-max-tokens");
    const maxTVal = document.querySelector("#set-max-tokens-val");
    if (maxT) {
      maxT.value = maxTokIndexFromVal(v.JANUS_MAX_TOKENS || "4096");
      if (maxTVal) maxTVal.textContent = fmtCtx(MAX_TOKEN_STEPS[maxT.value] || 4096) + " tokens";
    }
    const rp = document.querySelector("#set-repeat-penalty");
    const rpVal = document.querySelector("#set-repeat-penalty-val");
    if (rp) {
      const raw = v.JANUS_REPEAT_PENALTY || "1.1";
      rp.value = Math.round(parseFloat(raw) * 100);
      if (rpVal) rpVal.textContent = (parseFloat(raw)).toFixed(2);
    }
    const rln = document.querySelector("#set-repeat-last-n");
    const rlnVal = document.querySelector("#set-repeat-last-n-val");
    if (rln) {
      rln.value = v.JANUS_REPEAT_LAST_N || "64";
      if (rlnVal) rlnVal.textContent = rln.value + " tokens";
    }
    const hf = document.querySelector("#set-hf-token");
    if (hf) hf.value = v.HF_TOKEN || "";
    const lan = document.querySelector("#set-bind-lan");
    if (lan) lan.classList.toggle("on", v.JANUS_BIND_LAN === "true" || v.JANUS_BIND_LAN === "1");
    const local = document.querySelector("#set-local-only");
    if (local) local.classList.toggle("on", v.JANUS_LOCAL_ONLY === "true" || v.JANUS_LOCAL_ONLY === "1");
    const authSw = document.querySelector("#set-auth");
    const authPw = document.querySelector("#set-auth-password");
    if (authSw) {
      const on = v.JANUS_AUTH === "true" || v.JANUS_AUTH === "1";
      authSw.classList.toggle("on", on);
      if (authPw) authPw.style.display = on ? "" : "none";
    }
    if (authPw && v.JANUS_ADMIN_PASSWORD) authPw.value = v.JANUS_ADMIN_PASSWORD;
    setVal("#set-ollama-url", v.OLLAMA_BASE_URL);
    setVal("#set-openrouter-key", v.OPENROUTER_API_KEY);
    setVal("#set-openai-base", v.OPENAI_BASE_URL);
    setVal("#set-anthropic-key", v.ANTHROPIC_API_KEY);
    setVal("#set-gemini-key", v.GEMINI_API_KEY);
  }

  function setVal(sel, val) {
    const el = document.querySelector(sel);
    if (el && val) el.value = val;
  }

  async function saveSetting(key, value) {
    await j("/config/settings", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ values: { [key]: value } })
    });
    settingsCache[key] = value;
  }

  async function saveListenPort(port) {
    const host = settingsCache.JANUS_BIND_LAN === "true" || settingsCache.JANUS_BIND_LAN === "1" ? "0.0.0.0" : "127.0.0.1";
    await saveSetting("JANUS_LISTEN_ADDR", host + ":" + port);
  }

  function wireSettingsEvents() {
    document.querySelector("#set-defer-load")?.addEventListener("change", e =>
      saveSetting("JANUS_DEFER_MODEL_LOAD", e.target.value === "1" ? "1" : "0"));
    document.querySelector("#set-backend")?.addEventListener("change", e => saveSetting("INFERENCE_BACKEND", e.target.value));
    document.querySelector("#set-gpu-layers")?.addEventListener("change", e => {
      saveSetting("JANUS_GPU_LAYERS", e.target.value);
      const v = document.querySelector("#set-gpu-layers-val");
      if (v) v.textContent = e.target.value === "-1" ? "all" : e.target.value + " layers";
    });
    document.querySelector("#set-vram-ceiling")?.addEventListener("input", e => {
      const mb = e.target.value;
      const v = document.querySelector("#set-vram-ceiling-val");
      if (v) v.textContent = (mb / 1024).toFixed(1) + " GB";
    });
    document.querySelector("#set-vram-ceiling")?.addEventListener("change", e => saveSetting("JANUS_VRAM_CEILING_MB", e.target.value));
    document.querySelector("#set-ctx")?.addEventListener("input", e => {
      const n = CTX_SIZES[e.target.value] || 8192;
      const v = document.querySelector("#set-ctx-val");
      if (v) v.textContent = fmtCtx(n);
    });
    document.querySelector("#set-ctx")?.addEventListener("change", e => {
      saveSetting("JANUS_CTX_SIZE", String(CTX_SIZES[e.target.value] || 8192));
    });
    document.querySelector("#set-prompt-format")?.addEventListener("change", e => saveSetting("JANUS_PROMPT_FORMAT", e.target.value));
    document.querySelector("#set-think")?.addEventListener("click", async e => {
      const el = e.currentTarget;
      el.classList.toggle("on");
      await saveSetting("JANUS_THINK", el.classList.contains("on") ? "true" : "false");
    });
    document.querySelector("#set-max-tokens")?.addEventListener("input", e => {
      const n = MAX_TOKEN_STEPS[e.target.value] || 4096;
      const v = document.querySelector("#set-max-tokens-val");
      if (v) v.textContent = fmtCtx(n) + " tokens";
    });
    document.querySelector("#set-max-tokens")?.addEventListener("change", e =>
      saveSetting("JANUS_MAX_TOKENS", String(MAX_TOKEN_STEPS[e.target.value] || 4096)));
    document.querySelector("#set-repeat-penalty")?.addEventListener("input", e => {
      const v = document.querySelector("#set-repeat-penalty-val");
      if (v) v.textContent = (e.target.value / 100).toFixed(2);
    });
    document.querySelector("#set-repeat-penalty")?.addEventListener("change", e =>
      saveSetting("JANUS_REPEAT_PENALTY", String(e.target.value / 100)));
    document.querySelector("#set-repeat-last-n")?.addEventListener("input", e => {
      const v = document.querySelector("#set-repeat-last-n-val");
      if (v) v.textContent = e.target.value + " tokens";
    });
    document.querySelector("#set-repeat-last-n")?.addEventListener("change", e =>
      saveSetting("JANUS_REPEAT_LAST_N", e.target.value));
    document.querySelector("#set-hf-token")?.addEventListener("change", e => saveSetting("HF_TOKEN", e.target.value));
    document.querySelector("#set-ollama-url")?.addEventListener("change", e => saveSetting("OLLAMA_BASE_URL", e.target.value));
    document.querySelector("#set-openrouter-key")?.addEventListener("change", e => saveSetting("OPENROUTER_API_KEY", e.target.value));
    document.querySelector("#set-openai-base")?.addEventListener("change", e => saveSetting("OPENAI_BASE_URL", e.target.value));
    document.querySelector("#set-anthropic-key")?.addEventListener("change", e => saveSetting("ANTHROPIC_API_KEY", e.target.value));
    document.querySelector("#set-gemini-key")?.addEventListener("change", e => saveSetting("GEMINI_API_KEY", e.target.value));
    document.querySelector("#set-bind-lan")?.addEventListener("click", async e => {
      const el = e.currentTarget;
      el.classList.toggle("on");
      await saveSetting("JANUS_BIND_LAN", el.classList.contains("on") ? "true" : "false");
      const port = document.querySelector("#set-port")?.value || "8990";
      await saveListenPort(port);
    });
    document.querySelector("#set-local-only")?.addEventListener("click", async e => {
      const el = e.currentTarget;
      el.classList.toggle("on");
      await saveSetting("JANUS_LOCAL_ONLY", el.classList.contains("on") ? "true" : "false");
    });
    document.querySelector("#set-port")?.addEventListener("change", e => saveListenPort(e.target.value.trim()));
    document.querySelector("#set-auth")?.addEventListener("click", async e => {
      const el = e.currentTarget;
      el.classList.toggle("on");
      const on = el.classList.contains("on");
      const pw = document.querySelector("#set-auth-password");
      if (pw) pw.style.display = on ? "" : "none";
      await saveSetting("JANUS_AUTH", on ? "true" : "false");
      if (!on) await saveSetting("JANUS_ADMIN_PASSWORD", "");
    });
    document.querySelector("#set-auth-password")?.addEventListener("change", e => saveSetting("JANUS_ADMIN_PASSWORD", e.target.value));
    document.querySelector("#model-folder-add")?.addEventListener("click", addFolderPrompt);
    document.querySelector("#models-add-folder")?.addEventListener("click", addFolderPrompt);
    document.querySelector("#models-scan-pc")?.addEventListener("click", () => scanPC(true));
  }

  // "Scan this PC": searches every drive for model files and adds the folders it finds.
  let scanTimer = 0;
  async function scanPC(start) {
    const btn = document.querySelector("#models-scan-pc");
    try {
      const s = start ? (await j("/models/scan-pc", { method: "POST" })).scan : await j("/models/scan-pc");
      if (s.running) {
        if (btn) btn.disabled = true;
        setText("#scan-status", "Searching this PC… " + fmtCount(s.dirs_checked) + " folders checked");
        clearTimeout(scanTimer);
        scanTimer = setTimeout(() => scanPC(false), 1500);
        return;
      }
      if (btn) btn.disabled = false;
      if (s.finished && scanTimer) { // only report a scan this page watched
        scanTimer = 0;
        const n = (s.found || []).length;
        setText("#scan-status", n ? "Scan done: added " + n + (n === 1 ? " folder" : " folders") : "Scan done: no new folders");
        refreshModels();
        refreshModelFolders();
      }
    } catch (e) {
      if (btn) btn.disabled = false;
      if (start) setText("#scan-status", errText(e));
    }
  }

  async function addFolderPrompt() {
    const path = window.prompt("Folder path to scan for .gguf models:");
    if (!path) return;
    try {
      await j("/config/model-folders", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action: "add", path: path.trim() })
      });
    } catch (e) { alert(errText(e)); }
    refreshModelFolders();
    refreshModels();
  }

  function setText(sel, text) {
    const el = document.querySelector(sel);
    if (el) el.textContent = text;
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }
  function escapeAttr(s) { return escapeHtml(s).replace(/"/g, "&quot;"); }

  function copyText(text) {
    if (navigator.clipboard) navigator.clipboard.writeText(text);
  }

  async function refreshVersion() {
    try {
      const v = await j("/version");
      const foot = document.querySelector(".side-foot");
      if (foot) foot.innerHTML = "Janus " + (v.current || "?") + "<br>" + (v.build_commit || "local") + " · " + (v.build_time || "");
    } catch (_) {}
  }

  async function sendTest() {
    const input = document.querySelector("#p-test .input");
    const reply = document.querySelector("#p-test .reply");
    if (!input || !reply) return;
    reply.textContent = "…";
    const t0 = performance.now();
    try {
      const r = await fetch(API + "/v1/chat/completions", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Client-Name": "Janus Test" },
        body: JSON.stringify({ model: "local", messages: [{ role: "user", content: input.value }], stream: false })
      });
      const data = await r.json();
      reply.textContent = data.choices?.[0]?.message?.content || JSON.stringify(data);
      const ms = Math.round(performance.now() - t0);
      const kv = document.querySelector("#p-test .kv");
      if (kv) kv.innerHTML = '<div>Round trip<b>' + ms + " ms</b></div>";
      refreshRequestLog();
    } catch (e) {
      reply.textContent = String(e);
    }
  }

  document.addEventListener("DOMContentLoaded", () => {
    document.querySelector("#p-test .btn.primary")?.addEventListener("click", sendTest);
    document.querySelector("#hub-search")?.addEventListener("click", hubSearch);
    document.querySelector("#hub-q")?.addEventListener("keydown", e => { if (e.key === "Enter") hubSearch(); });
    document.querySelector("#snip-tabs")?.addEventListener("click", e => {
      const tab = e.target.closest(".tab");
      if (!tab) return;
      snipTab = tab.dataset.snip;
      renderSnippet();
    });
    document.querySelector("#snip-copy")?.addEventListener("click", () =>
      copyText(document.querySelector("#snip-code")?.textContent || ""));
    renderSnippet();
    document.querySelectorAll("#p-server .btn.sm").forEach(btn => {
      if (btn.textContent.trim() === "Copy") {
        btn.addEventListener("click", () => {
          const code = btn.closest(".field")?.querySelector(".code");
          if (code) copyText(code.textContent);
        });
      }
    });
    document.querySelector("#log-clear")?.addEventListener("click", async () => {
      await j("/config/request-log", { method: "DELETE" });
      refreshRequestLog();
    });
    wireSettingsEvents();
    document.querySelector("#reload-model")?.addEventListener("click", async () => {
      const path = settingsCache.JANUS_MODEL_PATH;
      if (path) await j("/models/load", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ model_path: path }) });
    });
    refreshTopBar();
    refreshModels();
    refreshVersion();
    refreshServerPanel();
    refreshRequestLog();
    refreshPeers();
    refreshSettingsForm();
    scanPC(false); // a first-run scan may already be going; show it and refresh the list when it ends
    setInterval(refreshTopBar, 8000);
    setInterval(refreshRequestLog, 4000);
    setInterval(refreshPeers, 12000);
    refreshDownloads();
    setInterval(refreshDownloads, 1500);
  });
})();
