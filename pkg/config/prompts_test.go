package config

import (
	"strings"
	"testing"
)

func TestDefaultChatPromptAllowsMedicalDocumentation(t *testing.T) {
	p := strings.ToLower(DefaultRolePrompts().DefaultChat)
	for _, want := range []string{
		"help with the user's request directly",
		"never wrap your answer in json unless explicitly asked.",
		"do not redact, anonymize, sanitize",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("default chat prompt missing %q", want)
		}
	}
	for _, avoid := range []string{
		"medical records",
		"clinical documentation",
		"not a licensed clinician",
	} {
		if strings.Contains(p, avoid) {
			t.Fatalf("default chat prompt should not contain %q", avoid)
		}
	}
}

