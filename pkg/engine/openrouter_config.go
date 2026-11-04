package engine

import (
	"os"
	"strings"
)

func OpenRouterBaseURL() string {
	base := strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL"))
	if base == "" {
		return "https://openrouter.ai/api/v1"
	}
	return strings.TrimRight(base, "/")
}

func OpenRouterChatCompletionsURL() string {
	base := OpenRouterBaseURL()
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}
