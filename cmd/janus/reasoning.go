package main

import "strings"

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

type reasoningSplitter struct {
	inThink    bool
	pending    string
	afterThink bool
}

func (s *reasoningSplitter) Push(piece string) (reasoning, content string) {
	buf := s.pending + piece
	s.pending = ""
	var r, c strings.Builder
	for buf != "" {
		tag := thinkOpen
		if s.inThink {
			tag = thinkClose
		}
		if i := strings.Index(buf, tag); i >= 0 {
			s.emit(buf[:i], &r, &c)
			s.inThink = !s.inThink
			if !s.inThink {
				s.afterThink = true
			}
			buf = buf[i+len(tag):]
			continue
		}
		hold := partialSuffix(buf, tag)
		s.emit(buf[:len(buf)-hold], &r, &c)
		s.pending = buf[len(buf)-hold:]
		break
	}
	return r.String(), c.String()
}

func (s *reasoningSplitter) Flush() (reasoning, content string) {
	var r, c strings.Builder
	s.emit(s.pending, &r, &c)
	s.pending = ""
	return r.String(), c.String()
}

func (s *reasoningSplitter) emit(text string, r, c *strings.Builder) {
	if s.inThink {
		r.WriteString(text)
		return
	}
	if s.afterThink {
		text = strings.TrimLeft(text, "\r\n")
		if text == "" {
			return
		}
		s.afterThink = false
	}
	c.WriteString(text)
}

func partialSuffix(s, tag string) int {
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

func splitReasoning(text string) (reasoning, content string, unfinished bool) {
	if !strings.Contains(text, thinkOpen) {
		if i := strings.Index(text, thinkClose); i >= 0 {
			return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+len(thinkClose):]), false
		}
	}
	var s reasoningSplitter
	r1, c1 := s.Push(text)
	r2, c2 := s.Flush()
	return strings.TrimSpace(r1 + r2), strings.TrimSpace(c1 + c2), s.inThink
}
