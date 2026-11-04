package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"janus/pkg/singleton"
)

const fileName = "janus_settings.json"

// File is the console settings document (env keys → string values).
type File struct {
	Values map[string]string `json:"values"`
}

// Known keys the console may read and write.
var KnownKeys = []string{
	"JANUS_MODEL_PATH",
	"JANUS_MODEL_SEARCH_PATHS",
	"JANUS_SCAN_USER_PATHS",
	"JANUS_PC_SCAN",
	"JANUS_PC_SCANNED_AT",
	"JANUS_DEFER_MODEL_LOAD",
	"JANUS_AUTO",
	"INFERENCE_BACKEND",
	"JANUS_GPU_LAYERS",
	"JANUS_VRAM_CEILING_MB",
	"JANUS_CTX_SIZE",
	"JANUS_PROMPT_FORMAT",
	"JANUS_THINK",
	"JANUS_MAX_TOKENS",
	"JANUS_REPEAT_PENALTY",
	"JANUS_REPEAT_LAST_N",
	"JANUS_LOCAL_ONLY",
	"JANUS_LIB_PATH",
	"JANUS_LLAMA_DLL_URL",
	"JANUS_LLAMA_DLL_SHA256",
	"OLLAMA_BASE_URL",
	"OLLAMA_MODEL",
	"OPENROUTER_API_KEY",
	"OPENROUTER_BASE_URL",
	"OPENROUTER_MODEL",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"OPENAI_MODEL",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_MODEL",
	"GEMINI_API_KEY",
	"HF_TOKEN",
	"JANUS_LISTEN_ADDR",
	"JANUS_BIND_LAN",
	"JANUS_NO_BROWSER",
	"JANUS_AUTH",
	"JANUS_ADMIN_PASSWORD",
	"JANUS_UPDATE_REPO",
	"JANUS_INSTALL_ROOT",
}

func settingsPath() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		return filepath.Join(dir, fileName)
	}
	return filepath.Join(singleton.InstallRoot(), fileName)
}

// Path returns where settings are stored on disk.
func Path() string {
	return settingsPath()
}

// Load reads janus_settings.json if present.
func Load() (*File, error) {
	path := settingsPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Values: map[string]string{}}, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Values == nil {
		f.Values = map[string]string{}
	}
	return &f, nil
}

// Save writes the file next to janus.exe.
func (f *File) Save() error {
	if f.Values == nil {
		f.Values = map[string]string{}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), b, 0o644)
}

// Apply sets process environment from saved values. Keys already set in the
// process environment (your shell, a launcher, or CI) are never overwritten.
func (f *File) Apply() {
	if f == nil || f.Values == nil {
		return
	}
	for _, k := range KnownKeys {
		if _, already := os.LookupEnv(k); already {
			continue
		}
		v, ok := f.Values[k]
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		_ = os.Setenv(k, v)
	}
}

// Snapshot returns effective values for the console (file wins over empty env for display).
func Snapshot() map[string]string {
	f, _ := Load()
	out := map[string]string{}
	for _, k := range KnownKeys {
		if f != nil && f.Values != nil {
			if v := strings.TrimSpace(f.Values[k]); v != "" {
				out[k] = v
				continue
			}
		}
		out[k] = strings.TrimSpace(os.Getenv(k))
	}
	return out
}

// MergePatch updates keys from the request body and saves.
func MergePatch(patch map[string]string) (*File, error) {
	f, err := Load()
	if err != nil {
		return nil, err
	}
	for k, v := range patch {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		allowed := false
		for _, known := range KnownKeys {
			if known == k {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			delete(f.Values, k)
			_ = os.Unsetenv(k)
		} else {
			f.Values[k] = v
			_ = os.Setenv(k, v)
		}
	}
	if err := f.Save(); err != nil {
		return nil, err
	}
	f.Apply()
	return f, nil
}

// IntEnv reads an integer env var with default.
func IntEnv(key string, def int) int {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
