package discover

import (
	"os"
	"path/filepath"
	"strings"
)

// ModelFolder is one directory Janus scans for .gguf files.
type ModelFolder struct {
	Path       string `json:"path"`
	ModelCount int    `json:"model_count"`
	Removable  bool   `json:"removable"`
	BuiltIn    bool   `json:"built_in"`
}

// ModelSearchDirs returns deduplicated folders scanned for local models.
func ModelSearchDirs() []string {
	return modelSearchDirs()
}

// ExtraModelSearchPaths reads comma-separated paths from JANUS_MODEL_SEARCH_PATHS.
func ExtraModelSearchPaths() []string {
	return parseExtraSearchPaths()
}

func parseExtraSearchPaths() []string {
	extra := strings.TrimSpace(os.Getenv("JANUS_MODEL_SEARCH_PATHS"))
	if extra == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(extra, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(p))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, filepath.Clean(p))
	}
	return out
}

// CountGGUFInDir counts distinct .gguf files under dir (same walk limits as ScanLocalGGUF).
func CountGGUFInDir(dir string) int {
	byPath := map[string]LocalModel{}
	scanDirForGGUF(dir, byPath)
	return len(byPath)
}

// ListModelFolders lists every search dir with model counts.
func ListModelFolders() []ModelFolder {
	extras := parseExtraSearchPaths()
	extraSet := map[string]bool{}
	for _, e := range extras {
		extraSet[strings.ToLower(filepath.Clean(e))] = true
	}
	dirs := modelSearchDirs()
	out := make([]ModelFolder, 0, len(dirs))
	for _, d := range dirs {
		key := strings.ToLower(filepath.Clean(d))
		removable := extraSet[key]
		out = append(out, ModelFolder{
			Path:       d,
			ModelCount: CountGGUFInDir(d),
			Removable:  removable,
			BuiltIn:    !removable,
		})
	}
	return out
}

// MergeExtraSearchPath appends path to JANUS_MODEL_SEARCH_PATHS (comma-separated).
func MergeExtraSearchPath(existing []string, path string) []string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return existing
	}
	key := strings.ToLower(path)
	for _, e := range existing {
		if strings.ToLower(filepath.Clean(e)) == key {
			return existing
		}
	}
	return append(existing, path)
}

// RemoveExtraSearchPath drops one path from the extra list (case-insensitive).
func RemoveExtraSearchPath(existing []string, path string) []string {
	key := strings.ToLower(filepath.Clean(strings.TrimSpace(path)))
	if key == "" {
		return existing
	}
	out := make([]string, 0, len(existing))
	for _, e := range existing {
		if strings.ToLower(filepath.Clean(e)) == key {
			continue
		}
		out = append(out, e)
	}
	return out
}
