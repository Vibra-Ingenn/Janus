# Contributing to Janus

Thanks for your interest in contributing to Janus! We're building a high-performance local LLM router and appreciate help from the community.

## How to Contribute

### Reporting Bugs
- Check existing issues first to avoid duplicates
- Provide clear reproduction steps
- Include your environment (Go version, OS, GPU model if applicable)
- Attach relevant logs or error messages

### Suggesting Features
- Describe the use case and why it's valuable
- Explain how it fits Janus's philosophy (local-first, zero Python dependency, Vulkan-native)
- Link to any related issues or discussions

### Submitting Code

#### Setup
1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR-USERNAME/janus.git`
3. Create a feature branch: `git checkout -b feat/your-feature-name`
4. Install Go 1.21+

#### Development
1. Make your changes
2. **On Windows, stop all running `janus.exe` before `go build`** — Go cannot overwrite a running executable; the old process keeps going (see README → Development)
3. Add tests for new functionality
4. Run tests: `go test ./...`
5. Ensure your code passes: `go vet ./...` and `go fmt ./...`
6. Keep commit messages clear and descriptive

#### Submitting a Pull Request
1. Ensure all tests pass locally
2. Push your branch to your fork
3. Open a PR with:
   - Clear title describing the change
   - Description of what and why (link related issues if any)
   - Test coverage explanation
   - Any breaking changes clearly noted
4. Respond to review feedback

## Code Style

- Follow idiomatic Go conventions
- Use `gofmt` for formatting
- Write tests alongside features
- Document public packages and types
- Keep functions focused and small

## Design Philosophy

When contributing, keep these principles in mind:

- **Local-first**: Features should work without cloud services
- **No Python**: No dependencies on Python, pip, virtualenv
- **Vulkan-native**: Prefer GPU acceleration where applicable
- **Single binary**: Aim for zero-dependency deployment
- **Goroutine concurrency**: Use Go's concurrency model, not threads

## Testing

- Write tests for new features
- Test on multiple platforms if possible (Windows, Linux, macOS)
- Include both happy-path and error cases
- Run `go test -cover ./...` to check coverage

## License

By contributing, you agree that your contributions are licensed under the MIT License.

## Questions?

Feel free to open a discussion or issue. We're happy to help!
