// Package hfhub searches Hugging Face for GGUF models and downloads them into Janus's models folder.
//
// Downloads go to "<name>.gguf.part" first and are renamed when complete, so the model scanner never
// lists a half-finished file. Pausing keeps the .part file; starting the same download again resumes it.
package hfhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BaseURL is the Hugging Face site; tests point it at a local server.
var BaseURL = "https://huggingface.co"

var apiClient = &http.Client{Timeout: 20 * time.Second}

// Repo is one search result.
type Repo struct {
	ID           string `json:"id"`
	Downloads    int64  `json:"downloads"`
	Likes        int64  `json:"likes"`
	LastModified string `json:"last_modified"`
}

// File is one downloadable model. Split models (name-00001-of-00003.gguf) are one File with several Parts.
type File struct {
	Name  string   `json:"name"`
	Quant string   `json:"quant"`
	Size  int64    `json:"size_bytes"`
	Parts []string `json:"parts"`
	Fit   string   `json:"fit"` // "yes", "partly", "too_big"
	Best  bool     `json:"best"`
}

func token() string { return strings.TrimSpace(os.Getenv("HF_TOKEN")) }

func get(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if t := token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Hugging Face: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Hugging Face answered %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Search lists GGUF repositories matching q. sortBy is "downloads" (default), "newest" or "likes".
func Search(ctx context.Context, q, sortBy string) ([]Repo, error) {
	key := "downloads"
	switch sortBy {
	case "newest":
		key = "lastModified"
	case "likes":
		key = "likes"
	}
	v := url.Values{}
	v.Set("search", strings.TrimSpace(q))
	v.Set("filter", "gguf")
	v.Set("sort", key)
	v.Set("direction", "-1")
	v.Set("limit", "40")
	v.Add("expand[]", "downloads")
	v.Add("expand[]", "likes")
	v.Add("expand[]", "lastModified")
	var raw []struct {
		ID           string `json:"id"`
		Downloads    int64  `json:"downloads"`
		Likes        int64  `json:"likes"`
		LastModified string `json:"lastModified"`
	}
	if err := get(ctx, BaseURL+"/api/models?"+v.Encode(), &raw); err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(raw))
	for _, r := range raw {
		out = append(out, Repo{ID: r.ID, Downloads: r.Downloads, Likes: r.Likes, LastModified: r.LastModified})
	}
	return out, nil
}

// Files lists the GGUF models in a repository, marked against vramBytes (the graphics memory budget).
func Files(ctx context.Context, repo string, vramBytes int64) ([]File, error) {
	if !validRepo(repo) {
		return nil, errors.New("not a Hugging Face repository name")
	}
	var tree []struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
		LFS  *struct {
			Size int64 `json:"size"`
		} `json:"lfs"`
	}
	if err := get(ctx, BaseURL+"/api/models/"+repo+"/tree/main?recursive=true", &tree); err != nil {
		return nil, err
	}
	byName := map[string]*File{}
	var order []string
	for _, e := range tree {
		if e.Type != "file" || !strings.HasSuffix(strings.ToLower(e.Path), ".gguf") {
			continue
		}
		size := e.Size
		if e.LFS != nil && e.LFS.Size > 0 {
			size = e.LFS.Size
		}
		name := splitBase(e.Path)
		f, ok := byName[name]
		if !ok {
			f = &File{Name: name, Quant: quantOf(name)}
			byName[name] = f
			order = append(order, name)
		}
		f.Size += size
		f.Parts = append(f.Parts, e.Path)
	}
	files := make([]File, 0, len(order))
	for _, n := range order {
		f := byName[n]
		sort.Strings(f.Parts)
		if isHelperFile(f.Name) {
			continue
		}
		f.Fit = Fit(f.Size, vramBytes)
		files = append(files, *f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Size > files[j].Size })
	for i := range files {
		if files[i].Fit == "yes" {
			files[i].Best = true // biggest file that fits
			break
		}
	}
	return files, nil
}

// Fit says whether a model file runs fully on the graphics card ("yes"), needs some of it on the CPU
// ("partly"), or is too big to be practical ("too_big"). About 1.5 GB is kept back for the context.
func Fit(fileBytes, vramBytes int64) string {
	const headroom = int64(1536) << 20
	switch {
	case vramBytes <= 0:
		return "partly"
	case fileBytes+headroom <= vramBytes:
		return "yes"
	case fileBytes <= vramBytes*2:
		return "partly"
	default:
		return "too_big"
	}
}

// VRAMBudgetBytes reads the same graphics memory budget the rest of Janus uses.
func VRAMBudgetBytes() int64 {
	const defaultMiB = 12083
	mb, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("JANUS_VRAM_CEILING_MB")), 10, 64)
	if err != nil || mb <= 0 {
		mb = defaultMiB
	}
	return mb << 20
}

func validRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, `\:?#%`) {
			return false
		}
	}
	return true
}

// splitBase turns "dir/Model-Q4_K_M-00001-of-00003.gguf" into "Model-Q4_K_M.gguf".
func splitBase(p string) string {
	name := filepath.Base(filepath.FromSlash(p))
	lower := strings.ToLower(name)
	if i := strings.LastIndex(lower, "-of-"); i > 6 && strings.HasSuffix(lower, ".gguf") {
		if j := strings.LastIndex(lower[:i], "-"); j > 0 && i-j == 6 {
			return name[:j] + ".gguf"
		}
	}
	return name
}

func quantOf(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	up := strings.ToUpper(base)
	for _, sep := range []string{"-", ".", "_"} {
		if i := strings.LastIndex(up, sep+"Q"); i >= 0 {
			return base[i+1:]
		}
		if i := strings.LastIndex(up, sep+"IQ"); i >= 0 {
			return base[i+1:]
		}
	}
	for _, q := range []string{"F16", "BF16", "F32"} {
		if strings.HasSuffix(up, q) {
			return q
		}
	}
	return base
}

// isHelperFile skips vision projectors and similar add-on files that aren't models on their own.
func isHelperFile(name string) bool {
	l := strings.ToLower(name)
	return strings.HasPrefix(l, "mmproj") || strings.Contains(l, "-mmproj")
}

// ---- downloads ----

// Job is one download, as the console sees it.
type Job struct {
	ID        string `json:"id"`
	Repo      string `json:"repo"`
	Name      string `json:"name"`
	Done      int64  `json:"done_bytes"`
	Total     int64  `json:"total_bytes"`
	Speed     int64  `json:"bytes_per_sec"`
	State     string `json:"state"` // downloading, paused, done, cancelled, failed
	Error     string `json:"error,omitempty"`
	Path      string `json:"path,omitempty"`
	parts     []string
	destDir   string
	cancel    context.CancelFunc
	keepParts bool
}

// Manager runs downloads one file at a time per job, several jobs at once.
type Manager struct {
	mu   sync.Mutex
	jobs map[string]*Job
	dl   *http.Client
}

func NewManager() *Manager {
	return &Manager{jobs: map[string]*Job{}, dl: &http.Client{Timeout: 0}}
}

func jobID(repo, name string) string { return repo + "/" + name }

// Start begins (or resumes) downloading a model into destDir.
func (m *Manager) Start(repo string, f File, destDir string) (*Job, error) {
	if !validRepo(repo) {
		return nil, errors.New("not a Hugging Face repository name")
	}
	if len(f.Parts) == 0 {
		return nil, errors.New("no files to download")
	}
	for _, p := range f.Parts {
		if strings.Contains(p, "..") || !strings.HasSuffix(strings.ToLower(p), ".gguf") {
			return nil, errors.New("unexpected file name")
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	id := jobID(repo, f.Name)
	m.mu.Lock()
	if j, ok := m.jobs[id]; ok && j.State == "downloading" {
		m.mu.Unlock()
		return j, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{ID: id, Repo: repo, Name: f.Name, Total: f.Size, State: "downloading", parts: f.Parts, destDir: destDir, cancel: cancel}
	m.jobs[id] = j
	m.mu.Unlock()
	go m.run(ctx, j)
	return j, nil
}

func (m *Manager) run(ctx context.Context, j *Job) {
	dir := j.destDir
	if len(j.parts) > 1 {
		dir = filepath.Join(j.destDir, strings.TrimSuffix(j.Name, filepath.Ext(j.Name)))
		_ = os.MkdirAll(dir, 0o755)
	}
	var first string
	var doneBefore int64
	for i, part := range j.parts {
		dest := filepath.Join(dir, filepath.Base(filepath.FromSlash(part)))
		if i == 0 {
			first = dest
		}
		n, err := m.fetch(ctx, j, part, dest, doneBefore)
		doneBefore += n
		if err != nil {
			m.mu.Lock()
			switch {
			case errors.Is(ctx.Err(), context.Canceled) && j.keepParts:
				j.State = "paused"
			case errors.Is(ctx.Err(), context.Canceled):
				j.State = "cancelled"
				removeParts(dir, j.parts)
			default:
				j.State, j.Error = "failed", err.Error()
			}
			j.Speed = 0
			m.mu.Unlock()
			return
		}
	}
	m.mu.Lock()
	j.State, j.Speed, j.Done, j.Path = "done", 0, j.Total, first
	m.mu.Unlock()
}

// fetch downloads one file to dest via dest+".part", resuming a partial file. It returns the file's size.
func (m *Manager) fetch(ctx context.Context, j *Job, part, dest string, doneBefore int64) (int64, error) {
	if st, err := os.Stat(dest); err == nil {
		m.setDone(j, doneBefore+st.Size(), 0)
		return st.Size(), nil
	}
	tmp := dest + ".part"
	var have int64
	if st, err := os.Stat(tmp); err == nil {
		have = st.Size()
	}
	u := BaseURL + "/" + j.Repo + "/resolve/main/" + escapePath(part)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	if t := token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := m.dl.Do(req)
	if err != nil {
		return have, err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		flags |= os.O_TRUNC
		have = 0
	case http.StatusUnauthorized, http.StatusForbidden:
		return have, errors.New("this model needs a Hugging Face token (Settings > Hugging Face token) and its licence accepted on huggingface.co")
	default:
		return have, fmt.Errorf("Hugging Face answered %s", resp.Status)
	}
	out, err := os.OpenFile(tmp, flags, 0o644)
	if err != nil {
		return have, err
	}
	buf := make([]byte, 512*1024)
	got := have
	tick, mark := time.Now(), got
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return got, werr
			}
			got += int64(n)
		}
		if el := time.Since(tick); el >= time.Second {
			m.setDone(j, doneBefore+got, int64(float64(got-mark)/el.Seconds()))
			tick, mark = time.Now(), got
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return got, rerr
		}
	}
	if err := out.Close(); err != nil {
		return got, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return got, err
	}
	m.setDone(j, doneBefore+got, 0)
	return got, nil
}

func (m *Manager) setDone(j *Job, done, speed int64) {
	m.mu.Lock()
	j.Done, j.Speed = done, speed
	m.mu.Unlock()
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

func removeParts(dir string, parts []string) {
	for _, p := range parts {
		_ = os.Remove(filepath.Join(dir, filepath.Base(filepath.FromSlash(p))+".part"))
	}
}

// Pause stops a download and keeps what was downloaded; Start resumes it.
func (m *Manager) Pause(id string) bool { return m.stop(id, true) }

// Cancel stops a download and removes the partial file.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if ok && j.State == "paused" {
		j.State = "cancelled"
		removeParts(j.destDir, j.parts)
		if len(j.parts) > 1 {
			removeParts(filepath.Join(j.destDir, strings.TrimSuffix(j.Name, filepath.Ext(j.Name))), j.parts)
		}
		m.mu.Unlock()
		return true
	}
	m.mu.Unlock()
	return m.stop(id, false)
}

func (m *Manager) stop(id string, keep bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.State != "downloading" {
		return false
	}
	j.keepParts = keep
	j.cancel()
	return true
}

// Jobs returns every download this session, newest state first.
func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	return out
}
