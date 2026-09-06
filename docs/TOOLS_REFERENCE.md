# Janus Tool Reference

Complete reference for all **30 built-in tools** plus **unlimited community tools** available in the Janus kernel. The AI model calls these tools via GBNF-constrained JSON — every built-in tool is pure Go, zero Python, zero external services.

Community tools let anyone add new capabilities by filling in 7 JSON fields — no Go code required. See [`COMMUNITY_TOOLS.md`](COMMUNITY_TOOLS.md) for the full guide.

## Architecture

```
User Request → Kernel Loop → AI selects tool → Go executes → Result back to AI → Next tool
                  ↓
          GBNF grammar constrains output to valid JSON:
          {"tool_call": {"name": "...", "arguments": {...}}}
```

**Design principle:** The AI spends tokens on DECISIONS (~30 tokens). Go does the HEAVY LIFTING (instant execution). This makes a 14B local model as reliable as GPT-4 for structured tasks.

---

## Tool Categories

| Category | Tools | Purpose |
|----------|-------|---------|
| **Filesystem** | read_file, write_file, list_dir, search_files, create_dir, delete | Read/write local files |
| **Smart** | scaffold, patch_file, append_file, multi_write | High-leverage code generation |
| **System** | run_command, calculate, get_time, done | Shell access, math, time |
| **Document — Universal** | auto_ingest, docx_extract, pdf_extract, pdf_diagnose, image_extract, ocr_extract, render_pdf, render_docx | Any document in, any format out |
| **Data Processing** | (custom extensions via Community tools) | Text transformation, filtering, conversion |
| **Community** | *(your tools here)* | Any capability — 7 fields, no code required |

---

## Filesystem Tools

### `read_file`
Reads a file and returns its content.
- `path` (string, required)
- Returns file content (truncated at 4 KB for large files)

### `write_file`
Writes content to a file (creates or overwrites). Creates parent directories automatically.
- `path` (string, required)
- `content` (string, required)

### `list_dir`
Lists files in a directory with sizes and modification times.
- `path` (string, required)

### `search_files`
Recursive search for files by glob pattern or content pattern.
- `path` (string, required) — root directory
- `pattern` (string, required) — glob or regex

### `create_dir`
Creates a directory and any missing parents.
- `path` (string, required)

### `delete`
Deletes a file or directory (recursive).
- `path` (string, required)

---

## Smart Tools

### `scaffold`
Generates a complete project skeleton. AI outputs ~30 tokens, Go generates all boilerplate.
- `lang` (string, required) — `go`, `python`, `javascript`, `html`, `rust`, `c`
- `name` (string, required) — project name (becomes directory under `workspace/`)
- `type` (string, optional) — `cli`, `web`, `lib`, `script`
- `modules` (string, optional) — comma-separated module names

### `patch_file`
Surgically replaces one block of text in an existing file.
- `path` (string, required)
- `old_string` (string, required) — exact text to find (must be unique in file)
- `new_string` (string, required) — replacement text

### `append_file`
Appends content to an existing file without overwriting.
- `path` (string, required)
- `content` (string, required)

### `multi_write`
Writes multiple files in a single tool call.
- `files` (array, required) — array of `{"path": "...", "content": "..."}` objects

---

## System Tools

### `run_command`
Executes a shell command and returns stdout + stderr. Runs via `cmd /C` on Windows.
- `command` (string, required)
- `timeout` (int, optional) — seconds (default: 30)
- Output truncated at 3000 chars; 120-second hard timeout

### `calculate`
Evaluates a math expression.
- `expression` (string, required) — e.g. `"(1234 * 5.6) / 100 + 42"`

### `get_time`
Returns current date and time in ISO 8601. No parameters.

### `done`
Signals task completion and returns a final answer.
- `result` (string, required) — final response text

---

## Document — Universal Tools

### `auto_ingest`
Detects file type and dispatches the right extraction tool automatically.
- `path` (string, required)
- Supports: PDF, DOCX, images, text, CSV, JSON, YAML

### `docx_extract`
Extracts full text from a Word document (.docx).
- `path` (string, required)

### `pdf_extract`
Extracts text from a PDF with automatic xref repair for malformed files.
- `path` (string, required)
- Falls back to byte-level text salvage if standard parsing fails

### `pdf_diagnose`
Inspects PDF structure and reports health without attempting extraction.
- `path` (string, required)
- Returns: page count, xref health, encryption status, object count, issues

### `image_extract`
Extracts metadata from image files (PNG, JPG, TIFF, BMP).
- `path` (string, required)

### `ocr_extract`
Runs OCR on an image to extract text. Requires Tesseract to be installed.
- `path` (string, required)
- `lang` (string, optional) — language code (default: `eng`)

### `render_pdf`
Generates a new PDF from markdown or plain text content.
- `content` (string, required)
- `output` (string, optional) — output path (default: `workspace/outputs/`)

### `render_docx`
Generates a new Word document from markdown or plain text.
- `content` (string, required)
- `output` (string, optional)

---

## Protocol Engine Endpoints

These HTTP endpoints drive the 7-point durable workflow system. See [`PROTOCOL_ENGINE.md`](PROTOCOL_ENGINE.md).

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/protocol/run` | Execute a single 7-point Protocol |
| POST | `/protocol/batch` | Execute multiple Protocols concurrently |
| GET | `/protocol/status/<id>` | Get result of a specific Protocol run |
| GET | `/protocol/list` | List all Protocol results since last restart |

---

## Community Tools

Community tools are user-defined capabilities stored in `data/community_tools.db`. They are registered alongside built-in tools at startup and look identical to the AI. No Go code required — just fill in 7 JSON fields.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/tools/community` | List all installed community tools |
| POST | `/tools/community/install` | Install a tool from JSON body |
| DELETE | `/tools/community/{name}` | Remove a tool |
| POST | `/tools/community/fetch` | Fetch and install a tool pack from a URL |
| GET | `/tools/community/export` | Export all tools as shareable JSON |
| GET | `/tools/community/export/{name}` | Export a single tool |
| POST | `/tools/community/toggle` | Enable or disable a tool |

See [`COMMUNITY_TOOLS.md`](COMMUNITY_TOOLS.md) for the full guide, examples, and the 7-box format reference.
