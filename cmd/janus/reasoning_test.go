package main

import "testing"

func TestSplitReasoningComplete(t *testing.T) {
	r, c, unfinished := split_reasoning("<think>\nadd them up\n</think>\n\nThe answer is 5.")
	if r != "add them up" || c != "The answer is 5." || unfinished {
		t.Fatalf("got r=%q c=%q unfinished=%v", r, c, unfinished)
	}
}

func TestSplitReasoningUnfinished(t *testing.T) {
	r, c, unfinished := split_reasoning("<think>still working on it")
	if r != "still working on it" || c != "" || !unfinished {
		t.Fatalf("got r=%q c=%q unfinished=%v", r, c, unfinished)
	}
}

func TestSplitReasoningNoThinking(t *testing.T) {
	r, c, unfinished := split_reasoning("Just an answer.")
	if r != "" || c != "Just an answer." || unfinished {
		t.Fatalf("got r=%q c=%q unfinished=%v", r, c, unfinished)
	}
}

func TestSplitReasoningCloseOnly(t *testing.T) {
	r, c, _ := split_reasoning("reasoning here</think>\n\nanswer")
	if r != "reasoning here" || c != "answer" {
		t.Fatalf("got r=%q c=%q", r, c)
	}
}

func TestSplitterTagsSplitAcrossPieces(t *testing.T) {
	pieces := []string{"<th", "ink>", "plan", "</", "thi", "nk>", "\n\n", "Hel", "lo <b>"}
	var s reasoning_splitter
	var r, c string
	for _, p := range pieces {
		pr, pc := s.Push(p)
		r += pr
		c += pc
	}
	fr, fc := s.Flush()
	r += fr
	c += fc
	if r != "plan" || c != "Hello <b>" {
		t.Fatalf("got r=%q c=%q", r, c)
	}
}

// Gemma 4 puts its thinking in a "thought" channel: <|channel>thought…<channel|>.
func TestSplitReasoningGemmaChannel(t *testing.T) {
	r, c, unfinished := split_reasoning("<|channel>thought\nThe user wants one sentence.<channel|>The sky looks blue because air scatters blue light most.")
	if r != "The user wants one sentence." || c != "The sky looks blue because air scatters blue light most." || unfinished {
		t.Fatalf("got r=%q c=%q unfinished=%v", r, c, unfinished)
	}
}

// With thinking off Gemma still opens an empty channel; nothing of it may reach the answer.
func TestSplitReasoningGemmaEmptyChannel(t *testing.T) {
	r, c, _ := split_reasoning("<|channel>thought\n<channel|>So, when they ran the benchmark, things got weird.")
	if r != "" || c != "So, when they ran the benchmark, things got weird." {
		t.Fatalf("got r=%q c=%q", r, c)
	}
}

func TestSplitterGemmaTagsSplitAcrossPieces(t *testing.T) {
	pieces := []string{"<|chan", "nel>thou", "ght\nplan", "<chan", "nel|>", "Hel", "lo"}
	var s reasoning_splitter
	var r, c string
	for _, p := range pieces {
		pr, pc := s.Push(p)
		r += pr
		c += pc
	}
	fr, fc := s.Flush()
	r += fr
	c += fc
	if r != "plan" || c != "Hello" {
		t.Fatalf("got r=%q c=%q", r, c)
	}
}

// gpt-oss writes its thinking in an "analysis" channel, then the answer in a "final" channel.
func TestSplitReasoningGptOssHarmony(t *testing.T) {
	r, c, unfinished := split_reasoning("<|channel|>analysis<|message|>Need one sentence.<|end|><|start|>assistant<|channel|>final<|message|>A router forwards packets between networks.")
	if r != "Need one sentence." || c != "A router forwards packets between networks." || unfinished {
		t.Fatalf("got r=%q c=%q unfinished=%v", r, c, unfinished)
	}
}
