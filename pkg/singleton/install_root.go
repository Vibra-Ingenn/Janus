package singleton

import (
	"os"
	"path/filepath"
	"strings"
)

// installRoot is the Janus install folder (parent of dist\ when janus.exe runs from dist).
func installRoot() string {
	if r := strings.TrimSpace(os.Getenv("JANUS_INSTALL_ROOT")); r != "" {
		return filepath.Clean(r)
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Clean(filepath.Dir(exe))
	if strings.EqualFold(filepath.Base(dir), "dist") {
		return filepath.Dir(dir)
	}
	return dir
}

// InstallRoot is the Janus project folder (parent of dist\ when janus.exe runs from dist).
func InstallRoot() string {
	return installRoot()
}

// WebUIPath resolves webui.html from the install root, not the process cwd.
func WebUIPath() string {
	root := installRoot()
	if root != "" {
		p := filepath.Join(root, "webui.html")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, "webui.html")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "webui.html"
}

// ConsoleScriptPath resolves console.js next to webui.html.
func ConsoleScriptPath() string {
	root := installRoot()
	if root != "" {
		p := filepath.Join(root, "console.js")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, "console.js")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "console.js"
}

func PathUnderRoot(path, root string) bool {
	return pathUnderRoot(path, root)
}

func pathUnderRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if !strings.EqualFold(path, root) && !strings.HasPrefix(strings.ToLower(path), strings.ToLower(root+string(os.PathSeparator))) {
		return false
	}
	return true
}

func isJanusExePath(path string) bool {
	return strings.EqualFold(filepath.Base(path), "janus.exe")
}
