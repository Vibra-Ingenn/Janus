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

// The model must not be told it is "Janus": it then introduces itself that way inside other apps.
func TestDefaultChatPromptHasNoName(t *testing.T) {
	if strings.Contains(strings.ToLower(DefaultRolePrompts().DefaultChat), "janus") {
		t.Fatal("default chat prompt must not name Janus")
	}
}
