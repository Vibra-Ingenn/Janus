package discover

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanLocalGGUF(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(models, "a.gguf")
	big := filepath.Join(models, "b.gguf")
	writeModel(t, small, minModelBytes+1)
	writeModel(t, big, minModelBytes+2)
	t.Setenv("JANUS_INSTALL_ROOT", dir)
	t.Setenv("JANUS_SCAN_USER_PATHS", "0")
	got := ScanLocalGGUF()
	if len(got) != 2 {
		t.Fatalf("want 2 models, got %d", len(got))
	}
	if got[0].Name != "b.gguf" {
		t.Fatalf("expected largest first, got %q", got[0].Name)
	}
}

func TestPickLocalGGUFRespectsBudget(t *testing.T) {
	models := []LocalModel{
		{Name: "huge.gguf", Path: "huge.gguf", Size: 20 * 1024 * 1024 * 1024},
		{Name: "small.gguf", Path: "small.gguf", Size: 512 * 1024 * 1024},
	}
	t.Setenv("JANUS_VRAM_CEILING_MB", "4096")
	got, ok := PickLocalGGUF(models)
	if !ok {
		t.Fatal("expected pick")
	}
	if got.Name != "small.gguf" {
		t.Fatalf("want smallest that fits, got %q", got.Name)
	}
}

func TestApplyStartupDefaultsPicksModel(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(models, "test.gguf")
	writeModel(t, path, minModelBytes+1)
	t.Setenv("JANUS_INSTALL_ROOT", dir)
	t.Setenv("JANUS_AUTO", "1")
	t.Setenv("JANUS_SCAN_USER_PATHS", "0")
	os.Unsetenv("JANUS_MODEL_PATH")
	os.Unsetenv("INFERENCE_BACKEND")

	snap := ApplyStartupDefaults()
	if snap.AppliedModelPath == "" {
		t.Fatal("expected applied model path")
	}
	if os.Getenv("INFERENCE_BACKEND") != "vulkan" {
		t.Fatalf("backend=%q", os.Getenv("INFERENCE_BACKEND"))
	}
}
