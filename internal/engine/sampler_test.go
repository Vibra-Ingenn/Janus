package engine

import "testing"

func TestSampleLogitsGreedy(t *testing.T) {
	logits := []float32{0.1, 2.0, 1.5, -1.0}
	got := sampleLogits(logits, SamplerParams{Temp: 0}, nil)
	if got != 1 {
		t.Fatalf("greedy pick = %d, want 1", got)
	}
}

func TestSampleLogitsRepeatPenaltyChangesGreedyPick(t *testing.T) {
	logits := []float32{0.1, 2.0, 1.9, -1.0}
	sp := SamplerParams{Temp: 0, RepeatPenalty: 1.1, RepeatLastN: 64}
	// Token 1 was just generated; 2.0/1.1 ≈ 1.82 < 1.9, so token 2 wins.
	if got := sampleLogits(logits, sp, []int32{1}); got != 2 {
		t.Fatalf("penalised greedy pick = %d, want 2", got)
	}
	// Penalty must not modify the caller's logits.
	if logits[1] != 2.0 {
		t.Fatalf("logits mutated: %v", logits)
	}
}

func TestSampleLogitsPenaltyPushesNegativeLogitsDown(t *testing.T) {
	logits := []float32{-1.0, -1.05}
	sp := SamplerParams{Temp: 0, RepeatPenalty: 1.1}
	// -1.0 * 1.1 = -1.1 < -1.05, so the unpenalised token 1 wins.
	if got := sampleLogits(logits, sp, []int32{0}); got != 1 {
		t.Fatalf("pick = %d, want 1", got)
	}
}

func TestSampleLogitsPenaltyOneIsNoop(t *testing.T) {
	logits := []float32{0.1, 2.0, 1.9}
	sp := SamplerParams{Temp: 0, RepeatPenalty: 1.0}
	if got := sampleLogits(logits, sp, []int32{1}); got != 1 {
		t.Fatalf("pick = %d, want 1", got)
	}
}

func TestSampleLogitsTemperatureStaysInTopP(t *testing.T) {
	// One dominant token: with top_p=0.5 it must always be chosen.
	logits := []float32{10, 0, 0, 0, 0}
	sp := SamplerParams{Temp: 0.7, TopP: 0.5}
	for i := 0; i < 200; i++ {
		if got := sampleLogits(logits, sp, nil); got != 0 {
			t.Fatalf("iteration %d picked %d, want 0", i, got)
		}
	}
}
