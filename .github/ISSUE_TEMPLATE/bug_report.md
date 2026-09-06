---
name: Bug report
about: Report a bug to help us improve Janus
title: '[BUG] '
labels: bug
assignees: ''

---

## Describe the bug
A clear and concise description of what the bug is.

## Steps to reproduce
1. Build: `go build -o dist/janus.exe ./cmd/auditverify`
2. Configure: Set `.env` variables...
3. Run: `.\dist\janus.exe`
4. Call endpoint or interact with...

## Expected behavior
What you expected to happen.

## Actual behavior
What actually happened (error message, wrong output, crash, etc.).

## Environment
- **OS**: Windows 11 / Linux / macOS
- **Go version**: (run `go version`)
- **GPU**: NVIDIA RTX 5070 / AMD Radeon / Intel Arc / None (CPU only)
- **Model**: (e.g., llama2-7b-q4_0.gguf)
- **Inference backend**: vulkan / cpu / ollama

## Logs
```
(Paste relevant logs or error messages here)
```

## Additional context
Any other context that might help debug this.
