package discover

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// PCScanState is what the "Scan this PC" job has done so far.
type PCScanState struct {
	Running     bool     `json:"running"`
	DirsChecked int      `json:"dirs_checked"`
	Found       []string `json:"found"` // folders with models that Janus wasn't already searching
	Started     string   `json:"started,omitempty"`
	Finished    string   `json:"finished,omitempty"`
}

var pcScan struct {
	mu    sync.Mutex
	state PCScanState
}

// PCScan returns the current state of the whole-PC scan.
func PCScan() PCScanState {
	pcScan.mu.Lock()
	defer pcScan.mu.Unlock()
	s := pcScan.state
	s.Found = append([]string(nil), s.Found...)
	return s
}

// skipDirNames are system and build folders that never hold user models.
var skipDirNames = map[string]bool{
	"windows": true, "program files": true, "program files (x86)": true, "programdata": true,
	"$recycle.bin": true, "system volume information": true, "recovery": true, "$windows.~bt": true,
	"$windows.~ws": true, "winsxs": true, "temp": true, "tmp": true, "node_modules": true, ".git": true,
	"lost+found": true, "__pycache__": true,
}

// skipTopNames are Linux system folders (/proc, /dev …), skipped only at / and never on Windows,
// where C:\Dev is a user folder.
var skipTopNames = map[string]bool{"proc": true, "sys": true, "dev": true, "run": true, "snap": true}

// StartPCScan walks every fixed drive in the background looking for model files, then calls
// onDone with the folders that hold models Janus wasn't already searching. Returns false if a
// scan is already running. Reads folder listings only; never opens model files.
func StartPCScan(onDone func(found []string)) bool {
	pcScan.mu.Lock()
	if pcScan.state.Running {
		pcScan.mu.Unlock()
		return false
	}
	pcScan.state = PCScanState{Running: true, Started: time.Now().Format(time.RFC3339)}
	pcScan.mu.Unlock()

	go func() {
		found := scanRoots(scanRootDirs(), modelSearchDirs())
		pcScan.mu.Lock()
		pcScan.state.Running = false
		pcScan.state.Found = found
		pcScan.state.Finished = time.Now().Format(time.RFC3339)
		pcScan.mu.Unlock()
		if onDone != nil {
			onDone(found)
		}
	}()
	return true
}

// scanRoots returns folders under roots that hold model files and aren't covered by known.
func scanRoots(roots, known []string) []string {
	folders := map[string]string{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				name := strings.ToLower(d.Name())
				if path != root && (skipDirNames[name] || (runtime.GOOS != "windows" && skipTopNames[name] && filepath.Dir(path) == filepath.Clean(root))) {
					return filepath.SkipDir
				}
				pcScan.mu.Lock()
				pcScan.state.DirsChecked++
				pcScan.mu.Unlock()
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".gguf") {
				return nil
			}
			if _, ok := ggufFileSize(path); !ok {
				return nil
			}
			dir := filepath.Dir(path)
			if !coveredBy(dir, known) {
				folders[strings.ToLower(dir)] = dir
			}
			return nil
		})
	}
	return collapseFolders(folders)
}

// coveredBy is true when dir is inside one of the search dirs, within the walk depth.
func coveredBy(dir string, known []string) bool {
	for _, k := range known {
		rel, err := filepath.Rel(k, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if rel == "." || strings.Count(rel, string(filepath.Separator)) < ggufWalkMaxDepth {
			return true
		}
	}
	return false
}

// collapseFolders drops a folder when its parent folder is also in the list.
func collapseFolders(folders map[string]string) []string {
	list := make([]string, 0, len(folders))
	for _, f := range folders {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) < len(list[j]) })
	var out []string
	for _, f := range list {
		if !coveredBy(f, out) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}
