package discover

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// minModelBytes skips tokenizer-only and test files (llama.cpp ships ggml-vocab-*.gguf of a few MB).
const minModelBytes = 50 << 20

func isJunkModelName(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "ggml-vocab")
}

// isEmbeddingName spots models that turn text into numbers for search; they can't chat.
func isEmbeddingName(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"embed", "minilm", "bge-", "e5-", "gte-"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// realKey identifies a file by where it really lives, so a link and its target count once.
func realKey(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return strings.ToLower(filepath.Clean(path))
}

// ggufFileSize returns the size of a usable model file, following links; ok is false for
// broken links, tokenizer files and anything too small to be a model.
func ggufFileSize(path string) (int64, bool) {
	if isJunkModelName(filepath.Base(path)) {
		return 0, false
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() < minModelBytes {
		return 0, false
	}
	return st.Size(), true
}

// ollamaModelsDir is Ollama's model store: OLLAMA_MODELS, else ~/.ollama/models.
func ollamaModelsDir() string {
	if d := strings.TrimSpace(os.Getenv("OLLAMA_MODELS")); d != "" {
		return filepath.Clean(d)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ollama", "models")
}

// ollamaName turns manifests/<host>/<namespace>/<model>/<tag> into the name Ollama shows (qwen3:8b).
func ollamaName(parts []string) string {
	if len(parts) < 4 {
		return strings.Join(parts, "/")
	}
	host, ns, model, tag := parts[len(parts)-4], parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
	name := model + ":" + tag
	switch {
	case host == "registry.ollama.ai" && ns == "library":
		return name
	case host == "registry.ollama.ai":
		return ns + "/" + name
	default:
		return host + "/" + ns + "/" + name
	}
}

// scanOllamaStore reads Ollama's manifests and adds each model's weights file (blobs/sha256-…,
// no .gguf extension) under the name Ollama uses.
func scanOllamaStore(dir string, byKey map[string]LocalModel) {
	if dir == "" {
		return
	}
	manifests := filepath.Join(dir, "manifests")
	_ = filepath.WalkDir(manifests, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var m struct {
			Layers []struct {
				MediaType string `json:"mediaType"`
				Digest    string `json:"digest"`
			} `json:"layers"`
		}
		if json.Unmarshal(raw, &m) != nil {
			return nil
		}
		rel, err := filepath.Rel(manifests, path)
		if err != nil {
			return nil
		}
		name := ollamaName(strings.Split(filepath.ToSlash(rel), "/"))
		for _, l := range m.Layers {
			if l.MediaType != "application/vnd.ollama.image.model" {
				continue
			}
			blob := filepath.Join(dir, "blobs", strings.Replace(l.Digest, ":", "-", 1))
			size, ok := ggufFileSize(blob)
			if !ok {
				continue
			}
			addModel(byKey, newLocalModel(blob, name, size, "ollama"))
		}
		return nil
	})
}

func newLocalModel(path, name string, size int64, source string) LocalModel {
	m := LocalModel{Path: filepath.Clean(path), Name: name, Size: size, Source: source}
	if isEmbeddingName(name) {
		m.Kind = "embedding"
	}
	m.info, _ = os.Stat(path)
	return m
}

// addModel adds m unless the same file is already listed (symlink, hard link, or another Ollama
// name for the same weights); then m's name is kept as an alias of the first entry.
func addModel(byKey map[string]LocalModel, m LocalModel) {
	key := realKey(m.Path)
	if _, seen := byKey[key]; !seen && m.info != nil {
		for k, other := range byKey {
			if other.Size == m.Size && other.info != nil && os.SameFile(other.info, m.info) {
				key = k
				break
			}
		}
	}
	first, seen := byKey[key]
	if !seen {
		byKey[key] = m
		return
	}
	if !sameModelName(first.Name, m.Name) && !containsFold(first.Aliases, m.Name) {
		first.Aliases = append(first.Aliases, m.Name)
		byKey[key] = first
	}
}

// sameModelName treats "Vibra-Llama_PRO:latest" and a link called "Vibra-Llama_PRO-latest.gguf" as one name.
func sameModelName(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSuffix(strings.ToLower(s), ".gguf")
		return strings.NewReplacer(":", "-", "_", "-").Replace(s)
	}
	return norm(a) == norm(b)
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if sameModelName(v, s) {
			return true
		}
	}
	return false
}

// nameCollisions adds the folder name when two different files share a file name
// (Unsloth exports are often all called llama-3.2-3b-instruct.Q4_K_M.gguf).
func nameCollisions(models []LocalModel) {
	count := map[string]int{}
	for _, m := range models {
		count[strings.ToLower(m.Name)]++
	}
	for i, m := range models {
		if count[strings.ToLower(m.Name)] > 1 {
			models[i].Name = m.Name + " (" + filepath.Base(filepath.Dir(m.Path)) + ")"
		}
	}
}
