package singleton

import (
	"path/filepath"
	"testing"
)

func TestPathUnderRoot(t *testing.T) {
	root := filepath.Clean(`C:\projects\janus`)
	ok := pathUnderRoot(filepath.Join(root, "dist", "janus.exe"), root)
	if !ok {
		t.Fatal("expected dist\\janus.exe under root")
	}
	if pathUnderRoot(`C:\other\janus.exe`, root) {
		t.Fatal("other install should not match")
	}
}

func TestInstallRootFromEnv(t *testing.T) {
	want := filepath.Clean(`C:\projects\janus`)
	t.Setenv("JANUS_INSTALL_ROOT", want)
	got := installRoot()
	if got != want {
		t.Fatalf("got %q", got)
	}
}
