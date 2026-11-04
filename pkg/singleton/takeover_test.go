package singleton

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameJanusFile(t *testing.T) {
	me := `C:\Janus\dist\janus.exe`
	for other, want := range map[string]bool{
		`C:\Janus\dist\janus.exe`:     true,
		`c:\janus\DIST\JANUS.EXE`:     true,
		`C:\Janus\dist\janus.old.exe`: true,
		`C:\Other\dist\janus.old.exe`: false,
		`C:\Other\dist\janus.exe`:     false,
	} {
		if got := sameJanusFile(other, me); got != want {
			t.Errorf("sameJanusFile(%q) = %v, want %v", other, got, want)
		}
	}
}

func TestShutdownRefusesBrowsersAndStrangers(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		header string
	}{
		{"no takeover header", "", ""},
		{"wrong exe", "", `C:\elsewhere\janus.exe`},
		{"from a web page", "https://example.com", selfExe()},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/instance/shutdown", nil)
		req.RemoteAddr = "127.0.0.1:5000"
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if c.header != "" {
			req.Header.Set(TakeoverHeader, c.header)
		}
		rec := httptest.NewRecorder()
		HandleShutdown(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", c.name, rec.Code)
		}
	}
}
