# Janus — Local LLM Server & OpenAI-Compatible API

Janus is a **single Go binary** that runs `.gguf` models on your machine (GPU or CPU) and exposes an **OpenAI-compatible API**. No Python, no Docker, no Ollama required.

**Use it your way:** call it from the **command line** (`curl`, PowerShell, scripts), wire it into **Cursor / Cline / any OpenAI client** — same local models, whatever workflow fits you.

---

## What you get

- **Local inference** — llama.cpp via Vulkan (AMD / Intel / NVIDIA) or CPU fallback
- **OpenAI-compatible API** — `/v1/chat/completions`, `/v1/models`
- **Hot-swap models** — change `.gguf` without restarting
- **Thinking model support** — `<think>` reasoning split into `reasoning_content`
- **Chat template auto-detection** — uses the template from GGUF metadata
- **Zero dependencies** — one `.exe` on Windows, no Python, no Docker

---

## Requirements

| Platform | What you need |
|----------|----------------|
| **Windows** (primary) | Windows 10/11, [Go 1.22+](https://go.dev/dl/), Vulkan-capable GPU recommended |
| **Linux** | Go 1.22+, Vulkan or CPU |
| **macOS** | Go 1.22+, CPU backend (Vulkan varies by hardware) |

**Disk:** plan for the model size (often 2–8 GB per model) plus ~50 MB for Janus + llama.dll.

---

## Quick start (Windows)

### 1. Clone and build

```powershell
git clone https://github.com/Vibra-Ingenn/Janus.git
cd Janus
.\build.ps1
```

`build.ps1` downloads pre-built **llama.cpp Vulkan DLLs** and compiles `dist\janus.exe`.

### 2. Download a model

Put a `.gguf` file in the `models\` folder. Use the included downloader:

```powershell
go build -o dist\modelget.exe .\cmd\modelget
.\dist\modelget.exe -repo meta-llama/Llama-3.2-3B-Instruct -file Llama-3.2-3B-Instruct-Q8_0.gguf -out .\models\
```

Or download any GGUF from [Hugging Face](https://huggingface.co/models?library=gguf).

### 3. Configure

```powershell
copy .env.example .env
```

Edit `.env`:

```env
INFERENCE_BACKEND=vulkan
JANUS_MODEL_PATH=./models/Llama-3.2-3B-Instruct-Q8_0.gguf
JANUS_MAX_TOKENS=4096
```

| Variable | Default | Meaning |
|----------|---------|---------|
| `INFERENCE_BACKEND` | `vulkan` | `vulkan`, `cpu`, or `openrouter` |
| `JANUS_MODEL_PATH` | *(required)* | Path to your `.gguf` file |
| `JANUS_GPU_LAYERS` | `-1` | `-1` = all layers on GPU, `0` = CPU only |
| `JANUS_VRAM_CEILING_MB` | `9216` | VRAM budget hint (MiB) |
| `JANUS_MAX_TOKENS` | `4096` | Max tokens per reply |
| `JANUS_LISTEN_ADDR` | `127.0.0.1:8990` | Bind address |

### 4. Run

```powershell
.\dist\janus.exe
```

Opens **http://127.0.0.1:8990** in your browser.

### 5. Verify

```powershell
curl http://127.0.0.1:8990/health
```

---

## Quick start (Linux / macOS)

```bash
git clone https://github.com/Vibra-Ingenn/Janus.git
cd Janus
go mod tidy
go build -o dist/janus ./cmd/janus
cp .env.example .env
# edit .env — set JANUS_MODEL_PATH and INFERENCE_BACKEND=cpu if no Vulkan
./dist/janus
```

On Linux you need `libllama.so` next to the binary or on `LD_LIBRARY_PATH`.

---

## OpenAI-compatible API

```bash
curl http://127.0.0.1:8990/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "local",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

**Base URL:** `http://127.0.0.1:8990/v1`

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness check (`?deep=true` for details) |
| GET | `/v1/models` | Model list |
| POST | `/v1/chat/completions` | Chat (streaming supported) |
| POST | `/models/load` | Hot-swap model |
| GET | `/models/list` | Available .gguf files |
| GET | `/engine/status` | VRAM and backend info |

### Streaming

```bash
curl http://127.0.0.1:8990/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"local","stream":true,"messages":[{"role":"user","content":"Tell me a joke"}]}'
```

---

## Connect to Cursor / Cline / other clients

```
Base URL:  http://127.0.0.1:8990/v1
API Key:   (leave blank)
```

---

## Pitfalls (learned the hard way)

| Problem | What's going on | Fix |
|---------|-----------------|-----|
| **"It built but my changes aren't there"** | On Windows, Go can't overwrite a running `.exe`. | Stop all `janus.exe` in Task Manager, then rebuild. |
| **"Address already in use"** | A leftover process holds port 8990. | Task Manager → end all `janus.exe`. |
| **Server starts, chat fails** | `JANUS_MODEL_PATH` wrong or no `.gguf` in `models/`. | Set the path in `.env`, put the file in `models/`. |
| **"local engine failed to start"** | Missing `llama.dll` or GPU driver issue. | Run `.\build.ps1`. Update GPU drivers, or set `INFERENCE_BACKEND=cpu`. |
| **Wrong URL** | Default is `http://127.0.0.1:8990`, not 8080. | Bookmark 8990. |
| **First reply takes forever** | Model loading into VRAM — normal. | Wait 10–60s; smaller quants (`Q4`) load faster. |

---

## Project layout

```
cmd/janus/          Main server (OpenAI-compatible API)
cmd/modelget/       Hugging Face model downloader
internal/engine/    llama.cpp Vulkan/CPU backend
internal/bridge/    DLL loader and FFI bindings
internal/singleton/ Single-instance guard
models/             Put .gguf files here (not committed)
dist/               janus.exe + llama.dll after build
```

---

## Development

```powershell
Get-Process -Name "janus" -ErrorAction SilentlyContinue | Stop-Process -Force
go test ./...
go build -o dist\janus.exe .\cmd\janus
.\dist\janus.exe
```

Or just `.\run.ps1`. For a full build including llama DLLs, use `.\build.ps1`.

Contributions welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md).

---

## License

MIT — see [`LICENSE`](LICENSE).
