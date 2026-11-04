package discover

import (
	"os"
	"path/filepath"
	"testing"
)

// writeModel makes a file of the given size without writing the bytes (sparse on most disks).
func writeModel(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestOllamaStoreFound(t *testing.T) {
	store := t.TempDir()
	writeModel(t, filepath.Join(store, "blobs", "sha256-abc"), minModelBytes+1)
	writeModel(t, filepath.Join(store, "blobs", "sha256-tmpl"), 10)
	manifest := `{"layers":[{"mediaType":"application/vnd.ollama.image.model","digest":"sha256:abc"},` +
		`{"mediaType":"application/vnd.ollama.image.template","digest":"sha256:tmpl"}]}`
	mdir := filepath.Join(store, "manifests", "registry.ollama.ai", "library", "qwen3")
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mdir, "8b"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OLLAMA_MODELS", store)
	t.Setenv("JANUS_INSTALL_ROOT", t.TempDir())
	t.Setenv("JANUS_SCAN_USER_PATHS", "")
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	got := ScanLocalGGUF()
	if len(got) != 1 || got[0].Name != "qwen3:8b" || got[0].Source != "ollama" {
		t.Fatalf("want one Ollama model qwen3:8b, got %+v", got)
	}
}

func TestScanSkipsJunkAndNamesCollisions(t *testing.T) {
	root := t.TempDir()
	models := filepath.Join(root, "models")
	writeModel(t, filepath.Join(models, "ggml-vocab-llama.gguf"), minModelBytes+1) // tokenizer file
	writeModel(t, filepath.Join(models, "tiny.gguf"), 1024)                        // too small to be a model
	writeModel(t, filepath.Join(models, "runA", "llama.Q4_K_M.gguf"), minModelBytes+1)
	writeModel(t, filepath.Join(models, "runB", "llama.Q4_K_M.gguf"), minModelBytes+2)
	writeModel(t, filepath.Join(models, "nomic-embed-text.gguf"), minModelBytes+3)
	t.Setenv("JANUS_INSTALL_ROOT", root)
	t.Setenv("JANUS_SCAN_USER_PATHS", "0")

	got := ScanLocalGGUF()
	names := map[string]LocalModel{}
	for _, m := range got {
		names[m.Name] = m
	}
	if len(got) != 3 {
		t.Fatalf("want 3 models, got %+v", got)
	}
	if _, ok := names["llama.Q4_K_M.gguf (runA)"]; !ok {
		t.Fatalf("same-name files should show their folder, got %+v", got)
	}
	if names["nomic-embed-text.gguf"].Kind != "embedding" {
		t.Fatalf("embedding model not marked: %+v", names["nomic-embed-text.gguf"])
	}
}

func TestLinkAndTargetCountOnce(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "models", "real.gguf")
	writeModel(t, target, minModelBytes+1)
	link := filepath.Join(root, "models", "links", "alias.gguf")
	_ = os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks need extra rights on this PC:", err)
	}
	broken := filepath.Join(root, "models", "links", "gone.gguf")
	_ = os.Symlink(filepath.Join(root, "missing.gguf"), broken)
	t.Setenv("JANUS_INSTALL_ROOT", root)
	t.Setenv("JANUS_SCAN_USER_PATHS", "0")

	if got := ScanLocalGGUF(); len(got) != 1 {
		t.Fatalf("want 1 model (link + target, broken link skipped), got %+v", got)
	}
}

func TestScanRootsFindsNewFolders(t *testing.T) {
	root := t.TempDir()
	known := filepath.Join(root, "known")
	writeModel(t, filepath.Join(known, "a.gguf"), minModelBytes+1)                            // already searched
	writeModel(t, filepath.Join(root, "Dev", "op", "models", "b.gguf"), minModelBytes+1) // new; "Dev" is only skipped at the top on Linux
	writeModel(t, filepath.Join(root, "Dev", "op", "models", "old", "c.gguf"), minModelBytes+1)
	writeModel(t, filepath.Join(root, "node_modules", "pkg", "d.gguf"), minModelBytes+1) // skipped folder
	writeModel(t, filepath.Join(root, "llama.cpp", "ggml-vocab-gpt-2.gguf"), minModelBytes+1)

	got := scanRoots([]string{root}, []string{known})
	want := filepath.Join(root, "Dev", "op", "models")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("want [%s], got %v", want, got)
	}
}
