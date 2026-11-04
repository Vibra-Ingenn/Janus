package main

import "strings"

const (
	think_open  = "<think>"
	think_close = "</think>"
)

// think_tag is one style of reasoning block: Qwen/DeepSeek <think>…</think>,
// Gemma 4's thought channel <|channel>thought…<channel|>, and gpt-oss's
// analysis channel <|channel|>analysis<|message|>…<|end|>, followed by the
// header of its final channel, which carries nothing to show.
type think_tag struct {
	open, close string
}

var think_tags = []think_tag{
	{think_open, think_close},
	{"<|channel>thought", "<channel|>"},
	{"<|channel|>analysis<|message|>", "<|end|>"},
	{"<|start|>assistant<|channel|>final", "<|message|>"},
}

// reasoning_splitter separates reasoning blocks from the answer in a stream
// of text pieces. Tags may arrive split across pieces, so any tail that could
// be the start of a tag is held back until the next Push.
type reasoning_splitter struct {
	in_think    bool
	close_tag   string // close tag of the block we are inside
	pending     string
	after_think bool // trim the blank lines models put right after a block
}

// Push consumes one piece and returns the reasoning and answer text that can
// be emitted now.
func (s *reasoning_splitter) Push(piece string) (reasoning, content string) {
	buf := s.pending + piece
	s.pending = ""
	var r, c strings.Builder
	for buf != "" {
		if s.in_think {
			if i := strings.Index(buf, s.close_tag); i >= 0 {
				s.emit(buf[:i], &r, &c)
				s.in_think = false
				s.after_think = true
				buf = buf[i+len(s.close_tag):]
				continue
			}
			hold := partial_suffix(buf, s.close_tag)
			s.emit(buf[:len(buf)-hold], &r, &c)
			s.pending = buf[len(buf)-hold:]
			break
		}
		at, tag := first_open_tag(buf)
		if at >= 0 {
			s.emit(buf[:at], &r, &c)
			s.in_think = true
			s.close_tag = tag.close
			buf = strings.TrimLeft(buf[at+len(tag.open):], "\r\n")
			continue
		}
		hold := 0
		for _, t := range think_tags {
			if n := partial_suffix(buf, t.open); n > hold {
				hold = n
			}
		}
		s.emit(buf[:len(buf)-hold], &r, &c)
		s.pending = buf[len(buf)-hold:]
		break
	}
	return r.String(), c.String()
}

// first_open_tag finds the earliest opening tag of any style in buf.
func first_open_tag(buf string) (int, think_tag) {
	best, found := -1, think_tag{}
	for _, t := range think_tags {
		if i := strings.Index(buf, t.open); i >= 0 && (best < 0 || i < best) {
			best, found = i, t
		}
	}
	return best, found
}

// Flush returns any held-back text at the end of the stream.
func (s *reasoning_splitter) Flush() (reasoning, content string) {
	var r, c strings.Builder
	s.emit(s.pending, &r, &c)
	s.pending = ""
	return r.String(), c.String()
}

func (s *reasoning_splitter) emit(text string, r, c *strings.Builder) {
	if s.in_think {
		r.WriteString(text)
		return
	}
	if s.after_think {
		text = strings.TrimLeft(text, "\r\n")
		if text == "" {
			return
		}
		s.after_think = false
	}
	c.WriteString(text)
}

// partial_suffix returns the length of the longest suffix of s that is a
// proper prefix of tag (i.e. a tag that may be completed by the next piece).
func partial_suffix(s, tag string) int {
	max := len(tag) - 1
	if len(s) < max {
		max = len(s)
	}
	for n := max; n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}

// split_reasoning splits a complete reply into reasoning and answer.
// unfinished is true when the reply ended inside a reasoning block (the model
// ran out of tokens before answering).
func split_reasoning(text string) (reasoning, content string, unfinished bool) {
	// Some templates open the block in the prompt, so the reply only closes it.
	for _, t := range think_tags {
		if !strings.Contains(text, t.open) {
			if i := strings.Index(text, t.close); i >= 0 {
				return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+len(t.close):]), false
			}
		}
	}
	var s reasoning_splitter
	r1, c1 := s.Push(text)
	r2, c2 := s.Flush()
	return strings.TrimSpace(r1 + r2), strings.TrimSpace(c1 + c2), s.in_think
}
