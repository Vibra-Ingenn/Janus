# Janus — Native Go LLM API Router & Model Runner

A high-performance Go platform that routes to local LLMs (via llama.cpp Vulkan/CPU) and cloud providers behind a single OpenAI-compatible API endpoint. Single binary, no Python, no Docker, no CGO.

**Design principle:** *The AI model makes requests. Go routes them, manages concurrency, and handles inference.*

**One endpoint → Many backends:** Route the same request to local Vulkan GPUs, CPU fallback, Ollama, OpenRouter, or Claude API — switch backends without client changes.

## What Can It Do?

- Run local `.gguf` models (llama2, Mistral, Qwen, DeepSeek) with Vulkan GPU acceleration
- Fall back to CPU or remote providers automatically on OOM
- Route multiple concurrent requests with goroutine concurrency (no Python GIL)
- Multiplexing OpenAI-compatible `/v1/chat/completions` endpoint
- Token budgeting and prompt deduplication to reduce inference cost
- Rate limiting per IP
- WebUI for chat and configuration
- Tool execution via OpenAI-compatible tool-use protocol

## Project Layout

```
cmd/modelget/           — Model downloader utility
internal/kernel/        — ReAct loop (AI + tool execution)
internal/tools/         — 20+ general-purpose tools
internal/engine/        — Vulkan/CPU inference via llama.cpp
internal/bridge/        — DLL loader for llama.cpp
internal/auth/          — API key authentication
internal/ratelimit/     — Token-bucket rate limiting
internal/validation/    — Input validation and safety checks
models/                 — Drop .gguf model files here
dist/                   — Built binary + llama.cpp DLLs
docs/                   — Architecture, deployment guides
```

## Quick Start

### 1. Build

```powershell
.\build.ps1                           # downloads llama.cpp DLLs
go build -o dist/janus.exe ./cmd/janus
```

### 2. Configure

Place a `.gguf` model in `models/` and edit `.env`:
```env
INFERENCE_BACKEND=vulkan
JANUS_MODEL_PATH=./models/your-model.Q8_0.gguf
JANUS_MAX_TOKENS=4096
```

### 3. Run

```powershell
.\dist\janus.exe
```

Open the WebUI at `http://localhost:8080` — tabs for Chat, Configuration, and Tool Management.

## Tool Suite (20+ tools)

| Category | Tool | What It Does |
|----------|------|-------------|
| **Filesystem** | `read_file` | Read file contents |
| | `write_file` | Write/overwrite file |
| | `list_dir` | List directory contents |
| | `search_files` | Glob search for files |
| | `create_dir` | Create directories |
| | `delete` | Delete file/empty dir |
| **Smart** | `scaffold` | Generate full project skeleton |
| | `patch_file` | Find/replace in file |
| | `append_file` | Append to file |
| | `multi_write` | Write multiple files in one call |
| **System** | `run_command` | Execute shell commands (safe mode) |
| | `calculate` | Math expressions |
| | `get_time` | Current date/time |
| | `done` | Signal task complete |
| **Universal Ingest** | `auto_ingest` | Auto-detect file type and parse |
| | `docx_extract` | Pure Go Word .docx → text |
| | `pdf_extract` | Pure Go PDF text extraction |
| **Universal Output** | `render_pdf` | Markdown → formatted PDF |
| | `render_docx` | Text → editable Word .docx |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness check (`?deep=true` for component checks) |
| POST | `/task/submit` | Submit task to kernel |
| POST | `/task/execute` | Execute plan |
| GET | `/v1/models` | OpenAI-compatible model list |
| POST | `/v1/chat/completions` | OpenAI-compatible chat (SSE streaming) |
| GET | `/v1/tools/list` | List all registered tools |
| POST | `/v1/tools/call` | Call a tool directly |
| POST | `/upload` | Upload document (multipart, 50 MB max) |
| GET | `/uploads/list` | List uploaded documents |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `INFERENCE_BACKEND` | `vulkan` | `vulkan`, `cpu`, or `ollama` |
| `JANUS_MODEL_PATH` | _(required)_ | Path to `.gguf` model file |
| `JANUS_GPU_LAYERS` | `-1` | GPU layers: -1=all, 0=CPU, N=first N |
| `JANUS_VRAM_CEILING_MB` | `9216` | VRAM budget in MiB for model planning |
| `JANUS_MAX_TOKENS` | `4096` | Max tokens per generation |
| `JANUS_PROMPT_FORMAT` | `chatml` | Prompt template format (chatml, llama2, etc.) |
| `JANUS_SAFE_MODE` | `false` | Block destructive/network commands |

## Architecture

```
User Request (OpenAI-compatible)
  │
  ├─ WebUI / API Endpoint
  │    │
  │    ├─ Kernel Loop (ReAct)
  │    │    ├─ AI selects tool (GBNF JSON)
  │    │    ├─ Go executes tool (instant)
  │    │    ├─ Result fed back
  │    │    └─ Repeat until "done"
  │    │
  │    └─ Inference Engine
  │         ├─ VulkanBackend (AMD/Intel/NVIDIA GPUs)
  │         ├─ CPUBackend (fallback)
  │         └─ OllamaBackend (WSL2, containers)
  │
  └─ Memory (SQLite)
       ├─ Sessions
       ├─ Messages
       ├─ Facts
       └─ Agent Lessons
```

## Features

- **Goroutine-based concurrency** — handles hundreds of concurrent requests
- **Automatic GPU OOM recovery** — retries with CPU fallback
- **SSE streaming** — real-time chat and multi-agent progress
- **Token budgeting** — plan inference costs before execution
- **Graceful shutdown** — 10s drain period for in-flight requests
- **Rate limiting** — per-IP token-bucket to prevent abuse
- **Session persistence** — SQLite-backed memory across restarts
- **Telemetry dashboard** — monitor latency, throughput, uptime

## Testing

```powershell
go test ./...    # 20+ tests
```

## Contributing

Contributions welcome! Please:
1. Fork and create a feature branch
2. Write tests for new functionality
3. Ensure `go test ./...` passes
4. Submit a pull request

## License

MIT — see LICENSE file.

## Notes

- Built with pure Go (no CGO except for llama.cpp bridge)
- Cross-platform: Windows (primary), Linux, macOS
- No Python, no virtualenv, no Docker required
- Single-file binary with embedded DLLs on Windows
