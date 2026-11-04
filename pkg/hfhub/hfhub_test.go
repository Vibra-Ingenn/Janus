package hfhub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const gb = int64(1) << 30

func fakeHub(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/models":
			if r.URL.Query().Get("filter") != "gguf" {
				t.Errorf("search must filter gguf, got %q", r.URL.RawQuery)
			}
			w.Write([]byte(`[{"id":"org/Tiny-GGUF","downloads":5,"likes":2,"lastModified":"2026-10-01T00:00:00.000Z"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/models/org/Tiny-GGUF/tree/main"):
			w.Write([]byte(`[
				{"type":"file","path":"README.md","size":10},
				{"type":"file","path":"Tiny-Q4_K_M.gguf","size":100,"lfs":{"size":` + itoa(4*gb) + `}},
				{"type":"file","path":"Tiny-Q8_0.gguf","size":100,"lfs":{"size":` + itoa(9*gb) + `}},
				{"type":"file","path":"Big/Tiny-F16-00001-of-00002.gguf","size":100,"lfs":{"size":` + itoa(10*gb) + `}},
				{"type":"file","path":"Big/Tiny-F16-00002-of-00002.gguf","size":100,"lfs":{"size":` + itoa(10*gb) + `}},
				{"type":"file","path":"mmproj-Tiny-f16.gguf","size":100}
			]`))
		case strings.HasPrefix(r.URL.Path, "/org/Tiny-GGUF/resolve/main/"):
			http.ServeContent(w, r, "x.gguf", time.Time{}, strings.NewReader(string(body)))
		default:
			http.NotFound(w, r)
		}
	}))
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old; srv.Close() })
	return srv
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestSearchAndFiles(t *testing.T) {
	fakeHub(t, nil)
	repos, err := Search(context.Background(), "tiny", "newest")
	if err != nil || len(repos) != 1 || repos[0].ID != "org/Tiny-GGUF" || repos[0].LastModified == "" {
		t.Fatalf("search: %v %+v", err, repos)
	}
	files, err := Files(context.Background(), "org/Tiny-GGUF", 12*gb)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 models (split F16 grouped, mmproj skipped), got %+v", files)
	}
	f16 := files[0]
	if f16.Name != "Tiny-F16.gguf" || len(f16.Parts) != 2 || f16.Size != 20*gb || f16.Fit != "partly" {
		t.Fatalf("split model wrong: %+v", f16)
	}
	q8, q4 := files[1], files[2]
	if q8.Fit != "yes" || !q8.Best || q4.Best || q4.Quant != "Q4_K_M" {
		t.Fatalf("best should be the biggest file that fits: q8=%+v q4=%+v", q8, q4)
	}
}

func TestFit(t *testing.T) {
	cases := []struct {
		file, vram int64
		want       string
	}{{4 * gb, 12 * gb, "yes"}, {11 * gb, 12 * gb, "partly"}, {30 * gb, 12 * gb, "too_big"}}
	for _, c := range cases {
		if got := Fit(c.file, c.vram); got != c.want {
			t.Errorf("Fit(%d,%d)=%s want %s", c.file, c.vram, got, c.want)
		}
	}
}

func TestRejectsBadRepo(t *testing.T) {
	for _, r := range []string{"", "a", "a/b/c", "../x", `a/..`, "a/b?x"} {
		if validRepo(r) {
			t.Errorf("accepted %q", r)
		}
	}
}

func waitState(t *testing.T, m *Manager, id, want string) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, j := range m.Jobs() {
			if j.ID == id && j.State == want {
				return j
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s: %+v", id, want, m.Jobs())
	return Job{}
}

func TestDownloadAndResume(t *testing.T) {
	content := []byte(strings.Repeat("gguf-bytes-", 5000))
	fakeHub(t, content)
	dir := t.TempDir()
	m := NewManager()
	f := File{Name: "Tiny-Q4_K_M.gguf", Size: int64(len(content)), Parts: []string{"Tiny-Q4_K_M.gguf"}}

	// A partial file from an earlier, paused download is resumed, not restarted.
	dest := filepath.Join(dir, "Tiny-Q4_K_M.gguf")
	if err := os.WriteFile(dest+".part", content[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	job, err := m.Start("org/Tiny-GGUF", f, dir)
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, m, job.ID, "done")
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != string(content) {
		t.Fatalf("downloaded file wrong (err %v, %d bytes)", err, len(got))
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part file should be gone after finishing")
	}
	if done.Path != dest {
		t.Fatalf("path %q", done.Path)
	}
}
