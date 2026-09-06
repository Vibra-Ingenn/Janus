# Changelog

All notable changes to Janus will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial open-source release
- Native Go LLM API router with OpenAI-compatible endpoints
- Vulkan GPU support via llama.cpp bridge
- CPU fallback inference
- Goroutine-based concurrency (no Python GIL)
- Token budgeting and cost planning
- Multi-agent orchestration with SSE streaming
- Session persistence via SQLite
- WebUI for chat, configuration, memory inspection
- Rate limiting and API key authentication
- 20+ general-purpose tools (filesystem, system, parsing, rendering)

### Removed
- Medical/healthcare domain code
- HIPAA compliance tools
- Document healing/recovery workflow
- Vibe Engine (workflow automation — separate project)
- Protocol orchestration engine (medical-specific)

## [Future Plans]

### Planned
- Ollama integration improvements
- OpenRouter multi-model routing
- Prompt optimization and caching
- Extended tool library
- Batch processing API
- Custom model fine-tuning support
- Docker containerization guide
