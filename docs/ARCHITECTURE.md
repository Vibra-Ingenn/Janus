# Janus architecture

Janus is a **standalone** local LLM host: Vulkan/CPU GGUF inference, OpenAI-compatible API, and the console web UI.

Other products (workflow engines, document tools, desktop clients) are **separate programs**. They may call Janus over HTTP when the user wants that backend. Janus does not embed their code, UI, or routes.

## Rules

1. **This repository is Janus only.** No connector source trees, proxy UIs, or private tooling.
2. **Connectors run their own process** and use Janus like any API client (`127.0.0.1:8990` by default).
3. **Janus runs without connectors.** Missing a connector must not change Janus behavior or UI.
4. **Connectors run without Janus.** They use Ollama, cloud APIs, or local engines as configured.

## Connector contract (HTTP)

| Endpoint | Use |
|----------|-----|
| `GET /health` | Liveness |
| `GET /v1/models` | Model IDs for chat |
| `POST /v1/chat/completions` | Inference (streaming supported) |
| `GET /models/list` | Local GGUF + configured backends |
| `POST /models/load` | Hot-swap GGUF (when local engine active) |
| `POST /models/unload` | Free VRAM |
| `GET /engine/status` | Backend + loaded model path |

Optional auth: set `JANUS_AUTH=false` for local connector use, or pass Basic Auth from the connector.

Configuration: `backend_config.json` (optional) lists named backends for routing; see repo example file.

## Examples (separate repos / folders)

- **Vibe Engine** — recipe runner on its own port; optional Janus client (`/v1/chat/completions`, `/models/list`).
- **Terminus** — document parsing; no Janus required; may add optional AI steps via HTTP later.

See [CONNECTORS.md](CONNECTORS.md) for implementers.
