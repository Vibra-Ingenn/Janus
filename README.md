# Janus — Local LLM server & OpenAI-compatible API

Janus is a **single Go binary** that runs `.gguf` models on your machine (GPU or CPU) and exposes an **OpenAI-compatible API** plus a built-in web UI. No Python, no Docker, no Ollama required — though Ollama is supported as a backend if you prefer.

**Use it your way:** call it from the **command line** (`curl`, PowerShell, scripts), wire it into **Cursor / Cline / any OpenAI client**, or use the **Web UI** — same local models, whatever workflow fits you. Janus is the runner and router; you choose the front end.

**Design idea:** the model decides what to do; Go runs inference, routes requests, executes tools, and keeps everything local.

---

## What you get

- **Local inference** — llama.cpp via Vulkan (AMD / Intel / NVIDIA) or CPU fallback
- **OpenAI-compatible API** — `/v1/chat/completions`, `/v1/models`, tool listing/calling
- **Web UI** — Assistant, Chat, Kernel (tool loop), Config, Memory, Skills
- **Built-in tools** — read/write files, run commands, math, docx in/out, PDF output, OCR (with Tesseract), and more
- **Hot-swap models** — change `.gguf` in the UI without restarting
- **Optional auth** — Basic Auth for admin endpoints when `JANUS_AUTH=true`

---

## Requirements

| Platform | What you need |
|----------|----------------|
| **Windows** (primary) | Windows 10/11, [Go 1.22+](https://go.dev/dl/), Vulkan-capable GPU recommended |
| **Linux** | Go 1.22+, Vulkan or CPU |
| **macOS** | Go 1.22+, CPU backend (Vulkan varies by hardware) |

**Disk:** plan for the model size (often 2–8 GB per model) plus ~50 MB for Janus + llama.dll.

**Optional:** [Tesseract OCR](https://github.com/tesseract-ocr/tesseract) if you want scanned-document OCR tools.

---

## Quick start (Windows)

### 1. Clone and build

```powershell
git clone https://github.com/YOUR_USERNAME/janus.git
cd janus
.\build.ps1
```

`build.ps1` downloads pre-built **llama.cpp Vulkan DLLs** and compiles `dist\janus.exe`.

If you already have DLLs in `lib\windows\`:

```powershell
.\build.ps1 -SkipDownload
```

### 2. Download a model

Put a `.gguf` file in the `models\` folder. Easiest path — use the included downloader:

```powershell
go build -o dist\modelget.exe .\cmd\modelget
.\dist\modelget.exe -repo meta-llama/Llama-3.2-3B-Instruct -file Llama-3.2-3B-Instruct-Q8_0.gguf -out .\models\
```

Or download any compatible GGUF from [Hugging Face](https://huggingface.co/models?library=gguf) manually.

### 3. Configure

```powershell
copy .env.example .env
```

Edit `.env` — **you must set the model path**:

```env
INFERENCE_BACKEND=vulkan
JANUS_MODEL_PATH=./models/Llama-3.2-3B-Instruct-Q8_0.gguf
JANUS_MAX_TOKENS=4096
JANUS_AUTH=false
```

| Variable | Default | Meaning |
|----------|---------|---------|
| `INFERENCE_BACKEND` | `vulkan` | `vulkan`, `cpu`, or `ollama` |
| `JANUS_MODEL_PATH` | *(required)* | Path to your `.gguf` file |
| `JANUS_GPU_LAYERS` | `-1` | `-1` = all layers on GPU, `0` = CPU only |
| `JANUS_VRAM_CEILING_MB` | `9216` | VRAM budget hint (MiB) |
| `JANUS_MAX_TOKENS` | `4096` | Max tokens per reply |
| `JANUS_LISTEN_ADDR` | `127.0.0.1:8990` | Bind address |
| `JANUS_AUTH` | `false` | Set `true` to require login on admin routes |
| `JANUS_SAFE_MODE` | `false` | Set `true` to block shell commands in tools |

### 4. Run

```powershell
.\dist\janus.exe
```

Or rebuild + launch in one step:

```powershell
.\run.ps1
```

Janus opens **http://127.0.0.1:8990** in your browser (disable with `JANUS_NO_BROWSER=1`).

First startup loads the model into VRAM — **expect 10–60 seconds** depending on model size and disk speed.

### 5. Verify

```powershell
curl http://127.0.0.1:8990/health
```

You should see `{"status":"ok",...}`.

---

## Quick start (Linux / macOS)

```bash
git clone https://github.com/YOUR_USERNAME/janus.git
cd janus
go mod tidy
go build -o dist/janus ./cmd/janus
cp .env.example .env
# edit .env — set JANUS_MODEL_PATH and INFERENCE_BACKEND=cpu if no Vulkan
./dist/janus
```

On Linux you need `libllama.so` next to the binary or on `LD_LIBRARY_PATH`. See `docs/AIR_GAPPED_INSTALL.md` for offline setup.

---

## Using the Web UI

After `.\dist\janus.exe` starts, your browser should open **http://127.0.0.1:8990** (or open that URL yourself). Wait for the status pill to show your model — first load can take 10–60 seconds.

### Everyday use (Assistant or Chat)

**Assistant** — best for “get this done” tasks:

1. Type what you want in the big text box
2. Optional: drag a file onto the upload zone or click attach
3. Click **Get Started**
4. Read the result; use **Copy** or start a **New Request**

**Chat** — best for back-and-forth conversation:

1. Open the **Chat** tab
2. Type a message and press Enter (Shift+Enter for a new line)
3. Pick a model from the dropdown if you have more than one

### Advanced tabs

Click **⚙ Advanced** in the top nav to reveal:

| Tab | What to do |
|-----|------------|
| **Kernel** | Type a task → **Run Kernel** → watch each tool call in the live log |
| **Config** | Pick a `.gguf` from the dropdown → **Save & Load Model**; edit the chat system prompt here |
| **Memory** | Browse past chats; save key/value **facts** the AI can reuse |
| **Skills** | Install community tools from JSON (optional power-user feature) |

### Switch models in the UI

1. **Advanced → Config**
2. Choose a model from the list (or paste a path to `./models/your-model.gguf`)
3. Click **Save & Load Model** — no restart needed

### Stop Janus

Focus the terminal where Janus is running and press **Ctrl+C**.

More walkthroughs (uploads, troubleshooting, tools): [`docs/USER_MANUAL.md`](docs/USER_MANUAL.md)

---

## OpenAI-compatible API

Janus is meant to stay out of your way — run prompts from a terminal, a script, or a third-party app by pointing it at Janus like any other OpenAI endpoint:

```bash
curl http://127.0.0.1:8990/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "local",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

**Base URL:** `http://127.0.0.1:8990/v1`  
**API key:** not required when `JANUS_AUTH=false`

### Useful endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness (`?deep=true` for component checks) |
| GET | `/v1/models` | Model list |
| POST | `/v1/chat/completions` | Chat (streaming supported) |
| GET | `/v1/tools/list` | Built-in + community tools |
| POST | `/v1/tools/call` | Call a tool directly |
| POST | `/kernel/run` | Run the ReAct kernel on a task |
| POST | `/upload` | Upload a file (multipart, 50 MB max) |

Full tool reference: [`docs/TOOLS_REFERENCE.md`](docs/TOOLS_REFERENCE.md)  
Operator guide: [`docs/USER_MANUAL.md`](docs/USER_MANUAL.md)

---

## Ollama backend (optional)

If you already use Ollama instead of local GGUF:

```env
INFERENCE_BACKEND=ollama
OLLAMA_BASE_URL=http://127.0.0.1:11434
OLLAMA_MODEL=mistral:7b
```

Janus proxies chat to Ollama; tools and the web UI still work.

---

## Pitfalls (learned the hard way)

If you're building or hacking on Janus, these are the gotchas that burned us repeatedly. None of this is obvious the first time.

| Problem | What’s going on | Fix |
|---------|-----------------|-----|
| **“It built but my changes aren’t there”** | On Windows, **Go can’t overwrite a running `.exe`**. Old `janus.exe` keeps running on the port. | End all **janus.exe** in Task Manager, or `.\run.ps1`, then rebuild. |
| **“Address already in use” / two Januses** | A leftover process (sometimes elevated) still holds port **8990**. Janus tries to clean this up, but an admin instance may survive. | Task Manager → end all **janus.exe**. Run your terminal as the same user that started the old one. |
| **Server starts, chat fails / no model** | `JANUS_MODEL_PATH` wrong, or no `.gguf` in `models/`. | Copy `.env.example` → `.env`, set the path, put the file in `models/`. Use **Config → Save & Load Model**. |
| **“local engine failed to start”** | Missing `llama.dll`, bad GPU drivers, or model path typo. | Run **`.\build.ps1`** first (copies DLLs into `dist/`). Update GPU drivers, or set `INFERENCE_BACKEND=cpu`. |
| **Built with only `go build`** | `go build` alone doesn’t fetch **llama.cpp DLLs**. | Use **`.\build.ps1`** for a full Windows build, or copy DLLs from `lib\windows\` into `dist\` yourself. |
| **Wrong URL** | Default is **`http://127.0.0.1:8990`**, not 8080. | Bookmark 8990. API base is `http://127.0.0.1:8990/v1`. |
| **“.env ignored”** | Janus searches **upward from current directory** and next to the exe. Running from the wrong folder loads a different `.env` (or none). | Run from project root, or keep `.env` beside `dist\janus.exe`. |
| **Paths to models break** | `./models/foo.gguf` is relative to **where you launch** Janus, not where the source code lives. | Launch from repo root, or use an absolute path in `.env`. |
| **First reply takes forever** | Model is loading into RAM/VRAM — normal. | Wait 10–60s; smaller quants (`Q4`) load faster than `Q8`. |
| **401 on Config / model load** | Auth is on (`JANUS_AUTH=true`). | Set `JANUS_AUTH=false` for local dev, or use the admin password printed on first run. |
| **OCR / PDF tools fail** | Tesseract not installed; PDF *input* is limited in this build. | Install Tesseract for scans; use Word/text uploads or `ocr_extract`. |
| **Committed secrets** | Never commit `.env` — it holds keys and passwords. | Only commit `.env.example`. |

If something still feels possessed, check **`logs/janus.log`** in the project folder and the terminal output from startup.

---

## Troubleshooting

### “Model not found” / server starts but chat fails

- Check `JANUS_MODEL_PATH` in `.env` matches a real file under `models/`
- Use **Config → Save & Load Model** in the web UI to pick from detected `.gguf` files
- Paths are relative to where you run `janus.exe` (usually project root or `dist/`)

### Slow first response

Normal — the model loads on first request or at startup. Smaller quantizations (`Q4`, `Q5`) load faster than `Q8`.

### Vulkan / GPU errors

1. Update GPU drivers  
2. Try CPU: `INFERENCE_BACKEND=cpu` and `JANUS_GPU_LAYERS=0`  
3. Or use Ollama backend (above)

### Port already in use / rebuilt but nothing changed

See the [pitfalls table](#pitfalls-learned-the-hard-way) — almost always a stale `janus.exe` on Windows, or the wrong port (default **8990**, not 8080). If another app owns the port, set `JANUS_LISTEN_ADDR=127.0.0.1:8991` in `.env`.

### Auth / 401 errors

When `JANUS_AUTH=true`, Janus prints an admin password on first run. Use it for Config/Model admin routes, or set `JANUS_ADMIN_PASSWORD` in `.env` before first launch.

### Tool errors (OCR, PDF input)

- **OCR** needs Tesseract installed and on `PATH`
- **PDF text extraction** in OSS is limited — use `ocr_extract` for scans, or attach plain text / Word files
- **Output PDFs** via `render_pdf` work without extra installs

---

## Project layout

```
cmd/janus/          Main server
cmd/modelget/       Hugging Face model downloader
internal/kernel/    ReAct loop (model + tools)
internal/tools/     Built-in and community tools
internal/engine/    llama.cpp Vulkan/CPU backend
internal/bridge/    DLL loader
models/             Put .gguf files here (not committed)
dist/               janus.exe + llama.dll after build
docs/               Manuals and references
```

---

## Development

Hacking on Janus is welcome. Read **[Pitfalls (learned the hard way)](#pitfalls-learned-the-hard-way)** first — especially the Go-on-Windows rule: **stop all `janus.exe` processes before `go build`**, or you’ll think your fix didn’t work.

Typical loop:

```powershell
Get-Process -Name "janus" -ErrorAction SilentlyContinue | Stop-Process -Force
go test ./...
go build -o dist\janus.exe .\cmd\janus
.\dist\janus.exe
```

Or just `.\run.ps1` (stop → build → run in one step). For a full Windows build including llama DLLs, use `.\build.ps1`.

Contributions welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md).

---

## Also from the same team: Vibe Engine PRO

Janus is **complete on its own** — nothing here is missing or locked behind a paywall. Use it from the CLI, scripts, Cursor, the Web UI, or any OpenAI-compatible client.

If you enjoy running local models and want to see what else is out there, the same team makes **[Vibe Engine PRO](https://adeptuscamini.com)** — a separate product focused on recipe-based automation (multi-step workflows, MCP, pipelines that run in Go without calling the model every step). It is **one option**, not a requirement. You can also extend Janus yourself via the API.

| | **Janus (this repo)** | **Vibe Engine PRO** |
|---|----------------------|---------------------|
| **What it is** | Free model runner & API router | Paid automation / recipe engine |
| **Price** | Free (MIT) | 7-day trial, then $8.99/month |

Vibe Engine connects to a local OpenAI-compatible endpoint — Janus works for that:

```text
Base URL:  http://127.0.0.1:8990/v1
API key:   (leave blank when JANUS_AUTH=false)
```

Early subscribers at **$8.99/month** stay at that rate if the listed price goes up later.

- [adeptuscamini.com](https://adeptuscamini.com) · [Download](https://adeptuscamini.com/download.html) · [Demo](https://adeptuscamini.com/demo.html)

Take a look if it sounds interesting. If Janus is all you need, that's fine too.

---

## License

MIT — see [`LICENSE`](LICENSE).
