# Janus User Manual

**A practical, task-oriented guide to using Janus for everyday work.**

This manual is written for operators and IT staff — not developers. If you want API specs and parameter tables, see [`TOOLS_REFERENCE.md`](TOOLS_REFERENCE.md) instead.

---

## Table of Contents

1. [What Is Janus?](#1-what-is-janus)
2. [Getting Started](#2-getting-started)
3. [The Three Ways to Talk to Janus](#3-the-three-ways-to-talk-to-janus)
4. [How Do I…? — Task Walkthroughs](#4-how-do-i--task-walkthroughs)
5. [Tool Catalog — What Each Tool Does](#5-tool-catalog--what-each-tool-does)
6. [Uploading Files](#6-uploading-files)
7. [Where Your Output Files Go](#7-where-your-output-files-go)
8. [Security](#8-security)
9. [Troubleshooting](#9-troubleshooting)
10. [FAQ](#10-faq)

---

## 1. What Is Janus?

Janus is a **local AI assistant** that runs entirely on your own computer. No data ever leaves the machine. It ships with built-in tools — file read/write, shell commands, Word in/out, OCR (with Tesseract), and more.

**The big idea:** You tell Janus what you need in plain English. The AI picks tools and runs them in a loop until the job is done.

**Run it your way:** Web UI, command line, or any OpenAI-compatible app pointed at `http://127.0.0.1:8990/v1`.

**Other options (not required):** Janus is fully usable on its own. The same team also makes [Vibe Engine PRO](https://adeptuscamini.com) — recipe-based automation that can plug into Janus at `http://127.0.0.1:8990/v1`. $8.99/month after a free trial if you want to explore it; otherwise the API is there to build on yourself. [Download](https://adeptuscamini.com/download.html)

### What Makes It Different

| Feature | Why It Matters |
|---------|----------------|
| **Local only** | Data never touches the cloud. Runs entirely on your machine. |
| **Any input** | Accepts Word docs, images, text files, PDFs, typed text, or copy-pastes. |
| **Any output** | Produces PDFs, Word files, JSON, text, encrypted content, or custom formats. |
| **Built-in tools** | The AI calls real Go functions (read/write files, run commands, docx, OCR, etc.) |

---

## 2. Getting Started

### Start Janus

From the project folder, double-click or run:

```powershell
.\dist\janus.exe
```

You'll see log lines like:

```
janus: local engine ready [vulkan]
janus: memory db ready
kernel: ready — single-brain loop with 30 tools
janus v0.1.0 listening on 127.0.0.1:8990
```

### Open the WebUI

Open **http://127.0.0.1:8990** in your browser. Main tabs:

| Tab | Purpose |
|-----|---------|
| **Assistant** | The main "get things done" workspace. Type a goal, upload files, get results. |
| **Chat** | Conversational — back-and-forth dialogue with memory. |
| **Kernel** *(Advanced)* | Run the tool loop directly and watch each step. |
| **Config** *(Advanced)* | Switch models, view VRAM, edit chat system prompt. |
| **Memory** | Browse stored facts and past sessions. |
| **Skills** | Install shareable community automations. |

### Stop Janus

In the terminal: press **Ctrl+C**. Janus drains cleanly and saves state.

### If you're modifying the code (Windows)

Before rebuilding with `go build`, **stop every `janus.exe`** — Go on Windows cannot replace a running executable, so the old binary keeps running and it looks like your fix failed. Use `.\run.ps1` or run `Get-Process janus | Stop-Process -Force` first. Full list: README → **Pitfalls (learned the hard way)**.

---

## 3. The Three Ways to Talk to Janus

### A. Assistant (recommended for most work)

One big text box plus a drag-and-drop upload zone. Type what you want, attach files if needed, click **Get Started**. Janus plans, runs tools, and delivers a finished result.

Best for:
- One-shot tasks ("summarize this PDF")
- File-heavy work (uploading, processing, producing output)
- "I need X done" — let Janus figure out the steps

### B. Chat

Traditional message-by-message conversation. Keeps a memory of the session so you can iterate.

Best for:
- Exploring ("what's the ICD-10 for this?")
- Multi-turn refinement ("make the letter shorter")
- Questions that don't need file processing

---

## 4. How Do I…? — Task Walkthroughs

### How do I extract text from a PDF?

1. Open the **Assistant** tab
2. Click **📎 Attach document** or drag the PDF into the upload zone
3. Type: *"Extract the text from this document and summarize it"*
4. Click **Get Started**

**What happens under the hood:** Janus calls `auto_ingest` or `ocr_extract` depending on file type. Scanned PDFs need **Tesseract** installed for OCR.

Plain-text PDF extraction is limited in this build — for scans, say *"OCR this document"* or install Tesseract. See [Troubleshooting](#9-troubleshooting).

---

### How do I create a PDF document?

1. In Assistant, type: *"Write a formal letter about X topic. Export as a PDF."*
2. Click **Get Started**

Janus composes the text, then calls `render_pdf`. The finished PDF lands in `workspace/outputs/` with a timestamped filename.

---

### How do I create an editable Word document?

Same as above, but say "Word document" or ".docx" instead of PDF. Janus calls `render_docx` — the file opens cleanly in Microsoft Word, LibreOffice, and Google Docs.

---

### How do I read text from a photo?

1. Snap a photo (PNG or JPG)
2. Upload to Assistant
3. Type: *"Read this image and summarize what it says"*

Janus calls `image_extract` → Tesseract OCR → summary. Quality depends on image legibility.

---

## 5. Tool Catalog — What Each Tool Does

Thirty tools, grouped by purpose. Click any tool to jump to its full reference in `TOOLS_REFERENCE.md`.

### 📁 Filesystem (6 tools)

| Tool | What It Does |
|------|--------------|
| `read_file` | Reads a file's contents — any text format. |
| `write_file` | Creates or overwrites a file. |
| `list_dir` | Lists files in a folder. |
| `search_files` | Glob-search for files by pattern (e.g. `*.pdf`). |
| `create_dir` | Creates a folder (and any missing parents). |
| `delete` | Deletes a file or empty directory. |

### 🧠 Smart Operations (4 tools)

| Tool | What It Does |
|------|--------------|
| `scaffold` | Generates a whole project skeleton in one call (~30 tokens → 10+ files). |
| `patch_file` | Applies diff-like edits — only the changed lines, never the whole file. |
| `append_file` | Adds content to the end of an existing file. |
| `multi_write` | Creates several files in one atomic call. |

### ⚙ System (4 tools)

| Tool | What It Does |
|------|--------------|
| `run_command` | Runs any PowerShell / shell command. Master key for anything else. |
| `calculate` | Safe arithmetic evaluator. |
| `get_time` | Current UTC timestamp. |
| `done` | Signals "task complete" and ends the reasoning loop. |


### 📥 Universal Ingest (4 tools)

These handle "any input" — for when you don't know the format or it's messy.

| Tool | What It Does |
|------|--------------|
| `auto_ingest` | **Start here for any upload.** Sniffs the file type and routes to the right parser. |
| `docx_extract` | Word `.docx` → plain text (pure Go, no Office install needed). |
| `ocr_extract` | Tesseract OCR for scanned PDFs and image-only faxes. |
| `image_extract` | OCR for photos of documents, whiteboards, handwritten notes. |

### 📤 Universal Output (2 tools)

These handle "any output" — polished files that clinicians can save or send.

| Tool | What It Does |
|------|--------------|
| `render_pdf` | Turns text / Markdown into a formatted PDF (Letter size, Helvetica). |
| `render_docx` | Turns text / Markdown into an editable Word `.docx`. |

---

## 6. Uploading Files

### Where

- **Assistant tab:** big drag-and-drop zone, or click **📎 Attach document**
- **Chat tab:** small 📎 button to the left of the message box

### Accepted File Types

| Category | Extensions |
|----------|-----------|
| Documents | `.pdf` `.docx` `.doc` `.rtf` `.odt` |
| Text & Data | `.txt` `.md` `.csv` `.tsv` `.json` `.xml` `.yaml` `.yml` `.hl7` `.html` `.log` |
| Spreadsheets | `.xlsx` `.xls` |
| Images (OCR) | `.png` `.jpg` `.jpeg` `.tif` `.tiff` `.bmp` `.gif` |
| Audio (reserved) | `.wav` `.mp3` `.m4a` `.flac` `.ogg` |

**Size cap:** 50 MB per file.

### Where Uploads Are Stored

All uploaded files go to `workspace/uploads/` with a timestamp prefix:

```
workspace/uploads/20260422-155642-patient-fax.pdf
```

They stay on your disk forever unless you delete them. Nothing is uploaded externally.

---

## 7. Where Your Output Files Go

Any file Janus *creates* (PDF, Word doc, FHIR JSON, etc.) is written to:

```
workspace/outputs/<timestamp>-<name>.<ext>
```

Examples:
```
workspace/outputs/20260422-163012-referral.pdf
workspace/outputs/20260422-163105-discharge-summary.docx
workspace/outputs/20260422-163228-patient-bundle.json
```

Janus will tell you the exact path in its reply. Open the file in File Explorer, Word, Adobe, or whatever you like.

---

## 8. Security

### Always On

- **Local-only processing** — inference runs on your GPU/CPU. No cloud calls.
- **Data stays local** — all files processed and stored on your machine only.
- **Encrypted at rest** — sensitive files can be encrypted using built-in encryption tools.

---

## 9. Troubleshooting

### "PDF parser crashed" or "cannot open PDF"

Usually means the PDF is **encrypted**, **corrupted**, or uses an unusual compression. Janus now tells the AI not to retry blindly — it will typically call `ocr_extract` as a fallback. If both fail, the PDF may need to be re-exported from the source system as plain text.

### "tesseract not installed on PATH"

OCR (`ocr_extract`, `image_extract`) requires Tesseract:

- **Windows:** install from <https://github.com/UB-Mannheim/tesseract/wiki>. The installer adds it to PATH automatically. Restart Janus after install.
- **Linux:** `sudo apt install tesseract-ocr`
- **macOS:** `brew install tesseract`

### "Janus isn't responding / spinning at 56 seconds"

Check `logs/janus.log`. If you see repeated tool failures, the AI is stuck retrying. Janus has a loop guard that forces the AI to stop after 3 consecutive failures of the same tool — but a very slow model may still take 30–60 seconds per iteration. Upgrade to a smaller/faster model in the **Config** tab if this is a regular issue.

### "My PDF has no text / `(no text found)`"

That PDF is **image-only** (a scan or photo converted to PDF without OCR). Ask Janus: *"Run OCR on this PDF"* — it will call `ocr_extract`.

### "The Word/PDF file didn't open correctly"

Check the file size in `workspace/outputs/`. If it's zero bytes, the render tool failed — check the logs. If it's a normal size but won't open, try a different viewer (Word vs. LibreOffice vs. Google Docs) — all three should handle the minimal OOXML Janus writes.

### "Model not loaded" / "Loading…" forever

The model file (`.gguf`) wasn't found at `JANUS_MODEL_PATH`. Open **Config** tab, pick your model, click **Save & Load Model**. Or run `.\dist\modelget.exe` to download one into `models\`.

### Browser keeps trying to open the uploaded PDF instead of uploading it

Hard-refresh the page (**Ctrl+F5**). The page's `<body>` has global `preventDefault` handlers that stop the browser from grabbing files — but you need a fresh copy if the HTML was cached.

---

## 10. FAQ

**Q: Does Janus require internet?**
A: Only for the initial model download (one time, ~5–10 GB). After that, everything is local. You can use it on an air-gapped machine.

**Q: How do I read an uploaded file?**
A: Say *"read this upload"* or *"open this file"*. Janus uses `auto_ingest` to detect the type and route to the right tool (`docx_extract`, `ocr_extract`, plain text, etc.). Scanned PDFs need Tesseract for OCR.

**Q: Can Janus read from a network drive or NAS?**
A: Yes — upload the file through the web UI, or pass a full path (e.g., `Z:\faxes\today.pdf`) in your prompt and Janus will read it directly.

**Q: How do I switch models?**
A: Open the **Config** tab. Click Browse, pick a `.gguf` file from `models/`, click Save. The swap is hot — no restart needed.

**Q: Can multiple people use Janus at once?**
A: Yes — each browser tab gets its own session. But inference is GPU-bound, so requests queue up behind each other on a single machine. For high throughput, run one Janus instance per workstation.

**Q: Where is my data stored?**
A: Four places:
- `workspace/uploads/` — files you upload
- `workspace/outputs/` — files Janus creates
- `logs/janus.log` — server log
- `logs/audit.jsonl` — HIPAA audit trail
- `janus.db` — SQLite with conversation history and long-term facts

All local. All deletable. Nothing ever leaves the machine.

**Q: Can I run Janus on a laptop without a GPU?**
A: Yes — it'll fall back to CPU inference. A 7B model runs at ~2–5 tokens/sec on a modern CPU, which is usable for short tasks but slow for long ones. A Vulkan-capable GPU (RTX 3060 or better, or AMD equivalent) is strongly recommended.

**Q: How do I update Janus?**
A: Pull the latest code, run `go build -o dist/janus.exe ./cmd/janus`, and restart. Config, logs, and the database are preserved.

**Q: How do I add my own tool?**
A: See [`TOOLS_REFERENCE.md`](TOOLS_REFERENCE.md#writing-a-custom-tool) for the developer guide.

---

## Need more detail?

- **API & parameter tables:** [`TOOLS_REFERENCE.md`](TOOLS_REFERENCE.md)
- **Project overview & build:** [`../README.md`](../README.md)
- **Demo script:** [`DEMO_KIT.md`](DEMO_KIT.md)

*Built for operators who want AI on their side of the firewall.*
