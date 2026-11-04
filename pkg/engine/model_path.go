package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveModelPath turns a UI or .env path into a clean absolute path.
func ResolveModelPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty model path")
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("model file not found: %s", path)
	}
	return path, nil
}

// DisplayModelPath returns a repo-relative path for UI display when possible.
func DisplayModelPath(absPath string) string {
	absPath = strings.TrimSpace(absPath)
	if absPath == "" {
		return ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(absPath)
	}
	if rel, err := filepath.Rel(cwd, absPath); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(absPath)
}

// SameModelFile reports whether the provider already has path loaded (resolved absolute path).
func SameModelFile(p Provider, path string) (bool, error) {
	resolved, err := ResolveModelPath(path)
	if err != nil {
		return false, err
	}
	amp, ok := p.(interface {
		ActiveModelPath() string
		ActiveModelLoaded() bool
	})
	if !ok || !amp.ActiveModelLoaded() {
		return false, nil
	}
	active := amp.ActiveModelPath()
	if active == "" {
		return false, nil
	}
	activeResolved, err := ResolveModelPath(active)
	if err != nil {
		return filepath.Clean(active) == filepath.Clean(resolved), nil
	}
	return filepath.Clean(activeResolved) == filepath.Clean(resolved), nil
}
