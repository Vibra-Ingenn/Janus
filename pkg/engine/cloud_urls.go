package engine

import (
	"os"
	"strings"
)

// AnthropicMessagesURL is the Messages API endpoint (override with ANTHROPIC_API_URL).
func AnthropicMessagesURL() string {
	if u := strings.TrimSpace(os.Getenv("ANTHROPIC_API_URL")); u != "" {
		return u
	}
	return "https://api.anthropic.com/v1/messages"
}
