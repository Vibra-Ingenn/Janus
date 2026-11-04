package discover

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// EnsureSharedInferenceLibrary copies or downloads the llama.cpp shared library when missing.
// Set JANUS_LLAMA_DLL_URL and JANUS_LLAMA_DLL_SHA256 (hex) to enable download.
// Returns the path to use (existing or newly written).
func EnsureSharedInferenceLibrary(preferredPath string) (string, error) {
	if preferredPath == "" {
		preferredPath = defaultSharedLibTarget()
	}
	if st, err := os.Stat(preferredPath); err == nil && st.Size() > 0 {
		return preferredPath, nil
	}

	url := strings.TrimSpace(os.Getenv("JANUS_LLAMA_DLL_URL"))
	wantSHA := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_LLAMA_DLL_SHA256")))
	if url == "" {
		return preferredPath, fmt.Errorf("shared library missing at %q (set JANUS_LLAMA_DLL_URL + JANUS_LLAMA_DLL_SHA256 to auto-download)", preferredPath)
	}

	if err := os.MkdirAll(filepath.Dir(preferredPath), 0o755); err != nil {
		return preferredPath, err
	}
	log.Printf("janus: downloading inference runtime to %s", preferredPath)

	client := &http.Client{Timeout: 20 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return preferredPath, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return preferredPath, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	tmp := preferredPath + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return preferredPath, err
	}
	hasher := sha256.New()
	writer := io.MultiWriter(out, hasher)
	if _, err := io.Copy(writer, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return preferredPath, err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return preferredPath, err
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if wantSHA != "" && got != wantSHA {
		os.Remove(tmp)
		return preferredPath, fmt.Errorf("SHA256 mismatch: got %s want %s", got, wantSHA)
	}
	if err := os.Rename(tmp, preferredPath); err != nil {
		return preferredPath, err
	}
	log.Printf("janus: inference runtime ready (%s)", preferredPath)
	return preferredPath, nil
}

func defaultSharedLibTarget() string {
	root := strings.TrimSpace(os.Getenv("JANUS_INSTALL_ROOT"))
	if root == "" {
		root = "dist"
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(root, "llama.dll")
	case "darwin":
		return filepath.Join(root, "libllama.dylib")
	default:
		return filepath.Join(root, "libllama.so")
	}
}
