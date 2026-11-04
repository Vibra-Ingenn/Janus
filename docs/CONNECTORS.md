# Connecting to Janus

Janus is an **optional** inference backend. Your application must run fully without it.

## Check Janus is up

```http
GET http://127.0.0.1:8990/health
```

## Chat (OpenAI-compatible)

```http
POST http://127.0.0.1:8990/v1/chat/completions
Content-Type: application/json

{
  "model": "local",
  "messages": [{"role": "user", "content": "Hello"}]
}
```

Use the `model` field for a backend id from `backend_config.json` when present, or a loaded GGUF name from `GET /models/list`.

## Discover models

```http
GET http://127.0.0.1:8990/models/list
GET http://127.0.0.1:8990/engine/status
```

## Load a local GGUF (admin)

```http
POST http://127.0.0.1:8990/models/load
Content-Type: application/json

{"model_path": "./models/your-model.gguf"}
```

Requires `JANUS_AUTH` off or valid admin credentials.

## Configuration

- Base URL: `JANUS_BASE_URL` or `http://127.0.0.1:8990` in your connector.
- Do not vendor Janus source into your repo; call the running process.

## Janus discovery API

```http
GET http://127.0.0.1:8990/config/connectors
```

Returns Janus role and documentation links for connector authors.
