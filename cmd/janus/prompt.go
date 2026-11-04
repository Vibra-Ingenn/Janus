package main

import (
	"fmt"
	"os"
	"strings"

	"janus/pkg/bridge"
	"janus/pkg/engine"
)

// chat_formatter is implemented by local GGUF backends that can render a
// conversation with the chat template embedded in the model file.
type chat_formatter interface {
	FormatChat(msgs []bridge.ChatMessage) (string, bool)
}

// prompt_format returns the JANUS_PROMPT_FORMAT override ("" = auto).
func prompt_format() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_PROMPT_FORMAT")))
}

// build_local_prompt renders a full conversation (system, user and assistant
// turns, in order) into a prompt for the local model.
//
// By default it uses the chat template stored in the model's GGUF metadata —
// the same template the model was trained with — and falls back to ChatML
// when the model has none. JANUS_PROMPT_FORMAT=chatml|llama2|alpaca forces a
// fixed format instead.
func build_local_prompt(p engine.Provider, msgs []oai_message) string {
	switch prompt_format() {
	case "llama2", "alpaca":
		system, user := split_messages(msgs)
		return format_local_prompt(system, user)
	case "chatml":
		return format_chatml(msgs)
	}

	if f, ok := p.(chat_formatter); ok {
		chat := make([]bridge.ChatMessage, 0, len(msgs))
		for _, m := range msgs {
			chat = append(chat, bridge.ChatMessage{Role: m.Role, Content: m.Content})
		}
		if out, ok := f.FormatChat(chat); ok {
			return out
		}
	}
	return format_chatml(msgs)
}

// format_chatml renders msgs as ChatML, ending with an open assistant turn.
func format_chatml(msgs []oai_message) string {
	var sb strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&sb, "<|im_start|>%s\n%s<|im_end|>\n", m.Role, m.Content)
	}
	sb.WriteString("<|im_start|>assistant\n")
	return sb.String()
}

// format_local_prompt combines a system instruction and user message into a
// single string prompt for local GGUF models.
// Format is selected via JANUS_PROMPT_FORMAT env var:
//
//	chatml   (default) — <|im_start|>/<|im_end|> turns (Qwen, many Mistral fine-tunes)
//	llama2            — [INST] <<SYS>> format for LLaMA 2 fine-tunes
//	alpaca            — ### Instruction / ### Response format
func format_local_prompt(system, user string) string {
	switch prompt_format() {
	case "llama2":
		return fmt.Sprintf("[INST] <<SYS>>\n%s\n<</SYS>>\n\n%s [/INST]", system, user)
	case "alpaca":
		return fmt.Sprintf("### Instruction:\n%s\n\n### Input:\n%s\n\n### Response:", system, user)
	default:
		return format_chatml([]oai_message{{Role: "system", Content: system}, {Role: "user", Content: user}})
	}
}
