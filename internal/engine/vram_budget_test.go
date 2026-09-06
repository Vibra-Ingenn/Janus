package engine

import "testing"

func TestVRAMCeilingBytesFromEnv(t *testing.T) {
	t.Setenv(VRAMCeilingEnvVar, "9728")
	got := VRAMCeilingBytesFromEnv()
	want := int64(9728 * 1024 * 1024)
	if got != want {
		t.Fatalf("VRAMCeilingBytesFromEnv() = %d, want %d", got, want)
	}
}

func TestVRAMCeilingBytesFromEnvFallsBackOnInvalidValue(t *testing.T) {
	t.Setenv(VRAMCeilingEnvVar, "nope")
	if got := VRAMCeilingBytesFromEnv(); got != DefaultVRAMCeiling {
		t.Fatalf("VRAMCeilingBytesFromEnv() = %d, want default %d", got, DefaultVRAMCeiling)
	}
}
