package bridge

import (
	"runtime"
	"unsafe"
)

// ChatMessage is one turn of a conversation passed to ApplyChatTemplate.
type ChatMessage struct {
	Role    string
	Content string
}

// llamaChatMessage mirrors struct llama_chat_message { const char *role; const char *content; }.
type llamaChatMessage struct {
	role    uintptr
	content uintptr
}

// cString returns a NUL-terminated copy of s. The caller must keep the
// returned slice alive for as long as the C side may read it.
func cString(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// ModelTemplate returns the chat template embedded in the model's GGUF
// metadata, or "" if the model has none or the DLL lacks the symbol.
func (lib *LlamaLib) ModelTemplate(model uintptr) string {
	if lib.ModelChatTemplate == nil || model == 0 {
		return ""
	}
	return lib.ModelChatTemplate(model, 0)
}

// ApplyChatTemplate renders msgs with the given template (as returned by
// ModelTemplate) and appends the assistant-turn header when addAssistant is
// true. ok is false when the DLL lacks the symbol, tmpl is empty, or
// llama.cpp does not recognise the template — callers should fall back.
func (lib *LlamaLib) ApplyChatTemplate(tmpl string, msgs []ChatMessage, addAssistant bool) (prompt string, ok bool) {
	if lib.ChatApplyTemplate == nil || tmpl == "" || len(msgs) == 0 {
		return "", false
	}

	tmplC := cString(tmpl)
	strs := make([][]byte, 0, len(msgs)*2) // keeps every C string alive
	chat := make([]llamaChatMessage, len(msgs))
	for i, m := range msgs {
		r, c := cString(m.Role), cString(m.Content)
		strs = append(strs, r, c)
		chat[i] = llamaChatMessage{
			role:    uintptr(unsafe.Pointer(&r[0])),
			content: uintptr(unsafe.Pointer(&c[0])),
		}
	}

	size := 0
	for _, m := range msgs {
		size += len(m.Role) + len(m.Content)
	}
	buf := make([]byte, size*2+512)

	for attempt := 0; attempt < 2; attempt++ {
		n := lib.ChatApplyTemplate(
			uintptr(unsafe.Pointer(&tmplC[0])),
			uintptr(unsafe.Pointer(&chat[0])),
			uintptr(len(chat)),
			addAssistant,
			uintptr(unsafe.Pointer(&buf[0])),
			int32(len(buf)),
		)
		runtime.KeepAlive(tmplC)
		runtime.KeepAlive(strs)
		runtime.KeepAlive(chat)
		runtime.KeepAlive(buf)
		if n < 0 {
			return "", false
		}
		if int(n) <= len(buf) {
			return string(buf[:n]), true
		}
		buf = make([]byte, n) // output was truncated: grow to the reported size
	}
	return "", false
}
